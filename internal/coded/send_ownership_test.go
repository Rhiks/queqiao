package coded

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestSendBufferOwnership(t *testing.T) {
	for _, owned := range []bool{false, true} {
		p := &Path{pending: make(chan []byte, 1), done: make(chan struct{})}
		frame := []byte("queued frame")
		send := p.SendContext
		if owned {
			send = p.SendOwnedContext
		}
		if err := send(context.Background(), frame); err != nil {
			t.Fatal(err)
		}
		queued := <-p.pending
		if !bytes.Equal(queued, frame) {
			t.Fatal("queue corrupted frame")
		}
		if (&queued[0] == &frame[0]) != owned {
			t.Fatalf("owned=%v: incorrect backing array", owned)
		}
		if !owned {
			frame[0] = 'X'
			if string(queued) != "queued frame" {
				t.Fatal("copying send retained caller's buffer")
			}
		}
	}
}

func TestSendOwnedKeepsFailureBoundaries(t *testing.T) {
	for _, test := range []string{"canceled", "closed", "full", "oversized"} {
		t.Run(test, func(t *testing.T) {
			p := &Path{pending: make(chan []byte, 1), done: make(chan struct{})}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			frame := []byte("frame")
			want := context.Canceled
			switch test {
			case "canceled":
				cancel()
			case "closed":
				close(p.done)
				want = ErrClosed
			case "full":
				p.pending <- []byte("occupied")
				go cancel()
			case "oversized":
				frame = make([]byte, maxFrameBytes+1)
				want = ErrFrameTooLarge
			}
			if err := p.SendOwnedContext(ctx, frame); !errors.Is(err, want) {
				t.Fatalf("error=%v want %v", err, want)
			}
			if test == "full" {
				if string(<-p.pending) != "occupied" {
					t.Fatal("failed send altered queue")
				}
			} else if len(p.pending) != 0 {
				t.Fatal("failed send enqueued frame")
			}
		})
	}
}
