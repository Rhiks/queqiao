package pep

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/bojieli/queqiao/internal/protocol"
)

// Age the previous proof explicitly rather than depending on a particular
// machine taking two seconds to carry several short exchanges. The first
// response on each new stream must leave the next borrower a warm path.
func TestNewPooledStreamResponseRenewsPathProof(t *testing.T) {
	rig := newJoinTestRig(t, TransportQUIC, TransportQUIC, 1)
	c := rig.client
	t.Cleanup(c.closeQUICPool)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cc := congestionConfig{kind: defaultCongestion()}
	outer, err := c.dialPooledQUICLane(ctx, cc)
	if err != nil {
		t.Fatal(err)
	}
	g := outer.(*controlPoolStreamConn).generation
	c.probeControlGenerationForTest = func(context.Context, *controlQUICGeneration, time.Duration) error {
		return errors.New("a completed exchange was ignored and an unnecessary probe started")
	}
	for i := 0; i < 3; i++ {
		lane := outer.(*controlPoolStreamConn)
		g.probeMu.Lock()
		g.verified = time.Time{}
		g.probeMu.Unlock()
		fc := newFrameConn(outer)
		frame := protocol.Frame{Header: protocol.Header{
			Version: protocol.Version, Type: protocol.TypeProbe,
			SessionID: [16]byte{byte(i + 1)}, Class: protocol.ClassNew,
		}, Payload: []byte{1}}
		if err := fc.Write(frame); err != nil {
			t.Fatal(err)
		}
		g.probeMu.Lock()
		verified := g.verified
		g.probeMu.Unlock()
		if !verified.IsZero() {
			t.Fatal("a local write was mistaken for remote proof")
		}
		response, err := fc.Read()
		if err != nil || response.Header.SessionID != frame.Header.SessionID {
			t.Fatalf("response = %+v, error = %v", response.Header, err)
		}
		g.probeMu.Lock()
		verified = g.verified
		g.probeMu.Unlock()
		if !verified.Equal(lane.proofStarted) {
			t.Fatalf("remote proof = %v, want stream creation %v", verified, lane.proofStarted)
		}
		if err := fc.Close(); err != nil {
			t.Fatal(err)
		}
		outer, err = c.dialPooledQUICLane(ctx, cc)
		if err != nil {
			t.Fatalf("warm borrow %d: %v", i, err)
		}
		if outer.(*controlPoolStreamConn).generation != g {
			t.Fatal("a successful short exchange replaced the pooled generation")
		}
	}
	outer.Close()
}

type proofReadQUICStream struct {
	closeTrackingQUICStream
	read func([]byte) (int, error)
}

func (s *proofReadQUICStream) Read(p []byte) (int, error) { return s.read(p) }

func TestPooledStreamProofCannotInventFreshness(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name     string
		started  time.Time
		verified time.Time
		failed   bool
		want     time.Time
	}{
		{name: "new response", started: now, want: now},
		{name: "delayed buffered response", started: now.Add(-2 * pooledPathProofMaxAge), want: now.Add(-2 * pooledPathProofMaxAge)},
		{name: "older response", started: now.Add(-time.Second), verified: now, want: now},
		{name: "response after invalidation", started: now, failed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := &controlQUICGeneration{verified: test.verified}
			stream := &proofReadQUICStream{read: func(p []byte) (int, error) { return copy(p, "x"), nil }}
			lane := &controlPoolStreamConn{
				quicStreamConn: &quicStreamConn{stream: stream}, generation: g,
				proofStarted: test.started, proofEpoch: g.proofEpoch,
			}
			if test.failed {
				g.invalidatePathProof()
			}
			if n, err := lane.Read(make([]byte, 1)); n != 1 || err != nil {
				t.Fatalf("read = %d, %v", n, err)
			}
			if !g.verified.Equal(test.want) {
				t.Fatalf("proof = %v, want %v", g.verified, test.want)
			}
			// Later reads of that same stream never extend the evidence or
			// revive it after a subsequent transport failure.
			g.invalidatePathProof()
			lane.Read(make([]byte, 1))
			if !g.verified.IsZero() {
				t.Fatal("another read on an old stream revived invalidated proof")
			}
		})
	}
}

func TestPooledStreamEmptyReadDoesNotConsumeRemoteProof(t *testing.T) {
	for _, emptyErr := range []error{nil, io.EOF} {
		g := &controlQUICGeneration{}
		stream := &proofReadQUICStream{read: func([]byte) (int, error) { return 0, emptyErr }}
		lane := &controlPoolStreamConn{
			quicStreamConn: &quicStreamConn{stream: stream}, generation: g,
			proofStarted: time.Now(),
		}
		lane.Read(make([]byte, 1))
		if !g.verified.IsZero() {
			t.Fatal("an empty read was mistaken for remote proof")
		}
		// A final positive read can return data and EOF together.
		stream.read = func(p []byte) (int, error) { return copy(p, "x"), io.EOF }
		lane.Read(make([]byte, 1))
		if !g.verified.Equal(lane.proofStarted) {
			t.Fatal("an empty read consumed the first positive response")
		}
	}
}
