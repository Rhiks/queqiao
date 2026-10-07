package pep

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/bojieli/queqiao/internal/protocol"
)

type eofFlowConn struct{ net.Conn }

func (*eofFlowConn) Read([]byte) (int, error) { return 0, io.EOF }

type gatedFINConn struct {
	*ackCaptureConn
	entered, release chan struct{}
	once             sync.Once
}

func (c *gatedFINConn) Write(p []byte) (int, error) {
	if len(p) >= protocol.HeaderSize {
		if header, err := protocol.DecodeHeader(p[:protocol.HeaderSize]); err == nil && header.Type == protocol.TypeClose {
			c.once.Do(func() { close(c.entered) })
			<-c.release
		}
	}
	return c.ackCaptureConn.Write(p)
}

func TestQueuedLaneFailureDoesNotDiscardPendingFINOnHealthyLane(t *testing.T) {
	inner, app := net.Pipe()
	defer app.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	flow := newMultipathFlow(ctx, &eofFlowConn{inner}, [16]byte{1}, 7, 1024,
		protocol.FlagAckUp, protocol.FlagAckDown, nil, nil)
	writer := &gatedFINConn{ackCaptureConn: newAckCaptureConn(0, nil), entered: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(writer.release) })
	primary := &mpLane{id: 0, fc: newFrameConn(writer)}
	secondary := &mpLane{id: 1, fc: newFrameConn(newAckCaptureConn(0, nil))}
	for _, lane := range []*mpLane{primary, secondary} {
		if err := flow.addLane(lane); err != nil {
			t.Fatal(err)
		}
	}
	flow.events <- inboundEvent{lane: primary, frame: protocol.Frame{Header: protocol.Header{
		Version: protocol.Version, Type: protocol.TypeClose, Flags: protocol.FlagFin,
		SessionID: flow.sessionID, FlowID: flow.flowID,
	}}}
	result := make(chan error, 1)
	go func() { _, err := flow.run(ctx); result <- err }()
	select {
	case <-writer.entered:
	case <-ctx.Done():
		t.Fatal("sender did not reach FIN write")
	}
	deadline := time.Now().Add(time.Second)
	for !flow.remoteFinSeen.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !flow.remoteFinSeen.Load() {
		t.Fatal("peer FIN was not consumed")
	}
	// A failure queued before FIN publication can be handled afterward. The
	// healthy lane still owns a FIN the peer has neither read nor acknowledged.
	flow.closeFailedLane(secondary)
	flow.laneErr <- laneFailure{lane: secondary, err: io.EOF}
	select {
	case err := <-result:
		t.Fatalf("flow discarded its healthy lane before FIN delivery: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if primary.closed.Load() {
		t.Fatal("healthy FIN writer was closed")
	}
	release.Do(func() { close(writer.release) })
	var sawACK, sawFIN bool
	for !sawACK || !sawFIN {
		select {
		case frame := <-writer.frames:
			switch frame.Header.Type {
			case protocol.TypeAck:
				sawACK = true
			case protocol.TypeClose:
				sawFIN = true
			default:
				t.Fatalf("unexpected closing frame %v", frame.Header.Type)
			}
		case <-ctx.Done():
			t.Fatal("FIN and final ACK were not delivered")
		}
	}
	if err := flow.receiveACK(inboundEvent{lane: primary, frame: protocol.Frame{Header: protocol.Header{
		Version: protocol.Version, Type: protocol.TypeAck, Flags: protocol.FlagAckUp | protocol.FlagAckFinal,
		SessionID: flow.sessionID, FlowID: flow.flowID,
	}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("flow did not finish after final acknowledgement")
	}
}
