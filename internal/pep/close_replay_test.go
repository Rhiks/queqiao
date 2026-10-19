package pep

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/bojieli/queqiao/internal/identity"
	"github.com/bojieli/queqiao/internal/metrics"
	"github.com/bojieli/queqiao/internal/protocol"
)

// A destination full-close can finish the server before the client sends its
// FIN. A subsequent JOIN must replay that abort, not invent a final ACK from
// the unset remoteFinSequence. Exercise both live and retained session paths.
func TestJoinedLaneReplaysAbortState(t *testing.T) {
	for _, completed := range []bool{false, true} {
		for _, peerFIN := range []bool{false, true} {
			t.Run(fmt.Sprintf("completed=%t/peerFIN=%t", completed, peerFIN), func(t *testing.T) {
				flow := newIsolationTestFlow(t, false)
				defer flow.closeAll()
				flow.noteLocalClose(6334)
				flow.localAbortSent.Store(true)
				if peerFIN {
					flow.remoteFinSequence.Store(6920)
					flow.remoteFinSeen.Store(true)
				}
				owner := identity.Principal{ProviderID: "p", AccountID: "a", DeviceID: "d"}
				sf := newServerFlow(flow, owner, TransportTCP, 1)
				server := &Server{
					cfg:      ServerConfig{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))},
					sessions: map[[16]byte]*serverFlow{flow.sessionID: sf}, metrics: metrics.New(),
				}
				if completed {
					server.retainCompletedSession(flow.sessionID, sf)
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				local, remote := net.Pipe()
				defer local.Close()
				defer remote.Close()
				_ = remote.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
				done := make(chan struct{})
				go func() {
					defer close(done)
					server.handleLaneJoinOpen(ctx, local, newFrameConn(local), owner, flow.sessionID, 1,
						protocol.Frame{Header: protocol.Header{FlowID: flow.flowID}})
				}()
				reader := newFrameConn(remote)
				open, err := reader.Read()
				if err != nil || open.Header.Type != protocol.TypeOpenOK {
					t.Fatalf("JOIN confirmation: %#v, %v", open.Header, err)
				}
				inner, app := net.Pipe()
				defer app.Close()
				client := newMultipathFlow(ctx, inner, flow.sessionID, flow.flowID, 1024,
					flow.recvAckFlag, flow.sendAckFlag, nil, nil)
				defer client.closeAll()
				client.noteSent(0, 6920)
				client.noteLocalClose(6920)
				frame, err := reader.Read()
				if err != nil {
					t.Fatalf("missing close-state replay: %v", err)
				}
				if peerFIN {
					if frame.Header.Type != protocol.TypeAck || frame.Header.Flags&protocol.FlagAckFinal == 0 {
						t.Fatalf("observed peer FIN was not acknowledged: %#v", frame.Header)
					}
					if err := client.receiveACK(inboundEvent{frame: frame}); err != nil {
						t.Fatal(err)
					}
					frame, err = reader.Read()
					if err != nil {
						t.Fatalf("missing abort after final ACK: %v", err)
					}
				} else if frame.Header.Type == protocol.TypeAck {
					t.Fatalf("invented final ACK for unseen peer FIN: %v", client.receiveACK(inboundEvent{frame: frame}))
				}
				if frame.Header.Type != protocol.TypeClose || frame.Header.Flags != protocol.FlagFin|protocol.FlagCloseAbort || frame.Header.Sequence != 6334 {
					t.Fatalf("abort replay changed: %#v", frame.Header)
				}
				client.events <- inboundEvent{frame: frame}
				if err := client.receiveInner(ctx); err != nil || !client.remoteAbort.Load() {
					t.Fatalf("replayed abort did not cancel the peer: %v", err)
				}
				if !peerFIN && client.acked != 0 {
					t.Fatal("abort falsely credited application delivery")
				}
				cancel()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("JOIN handler remained after cancellation")
				}
			})
		}
	}
}
