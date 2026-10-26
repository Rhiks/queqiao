package pep

import (
	"context"
	"io"
	"log/slog"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/apernet/quic-go"
	"github.com/bojieli/queqiao/internal/metrics"
)

// The prewarm has sent its padding when a link change interrupts the reader.
// No malformed response was received, so this cannot condemn the gateway.
type interruptedProbeConn struct {
	net.Conn
	err error
}

func (c *interruptedProbeConn) Read([]byte) (int, error)    { return 0, c.err }
func (c *interruptedProbeConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *interruptedProbeConn) CloseWrite() error           { return nil }

func TestPrewarmTransportInterruptionIsNotPeerViolation(t *testing.T) {
	for name, readErr := range map[string]error{
		"address lost":      &net.OpError{Op: "write", Net: "udp", Err: syscall.EADDRNOTAVAIL},
		"local pool reset":  &quic.ApplicationError{ErrorMessage: "queqiao client pool reset"},
		"closed connection": net.ErrClosed,
		"canceled context":  context.Canceled,
		"canceled stream":   &quic.StreamError{StreamID: 7},
		"closed pipe":       io.ErrClosedPipe,
	} {
		t.Run(name, func(t *testing.T) {
			local, remote := net.Pipe()
			defer local.Close()
			defer remote.Close()
			outer := &interruptedProbeConn{Conn: local, err: readErr}
			lane := &authenticatedLane{outer: outer, fc: newFrameConn(outer), sessionID: [16]byte{1}}
			defer lane.fc.Close()
			client := &Client{cfg: ClientConfig{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, metrics: metrics.New()}
			client.probePath(context.Background(), lane)
			if got := client.metrics.Snapshot().PeerProtocolViolations; got != 0 {
				t.Fatalf("transport interruption counted as %d peer protocol violations", got)
			}
		})
	}
}

func TestRejectProbePeerRetiresOnlyItsGeneration(t *testing.T) {
	for _, stale := range []bool{false, true} {
		name := "current generation"
		if stale {
			name = "late result from old generation"
		}
		t.Run(name, func(t *testing.T) {
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
			lane := &authenticatedLane{outer: outer, fc: newFrameConn(outer)}
			defer lane.fc.Close()
			old := outer.(*controlPoolStreamConn).generation
			var current *controlQUICGeneration
			if stale {
				c.drainControlQUICGeneration(old)
				replacement, err := c.dialPooledQUICLane(ctx, cc)
				if err != nil {
					t.Fatal(err)
				}
				defer replacement.Close()
				current = replacement.(*controlPoolStreamConn).generation
			}
			bulk, err := c.reserveBulkConn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			c.rejectProbePeer(lane, probeEchoViolation{reason: "invalid authenticated echo"})
			if old.conn.Context().Err() == nil {
				t.Error("offending generation was not retired")
			}
			if bulk.conn.Context().Err() != nil {
				t.Error("probe rejection closed an unrelated bulk connection")
			}
			c.quicMu.Lock()
			retained := c.quicGeneration
			c.quicMu.Unlock()
			if retained != current {
				t.Error("late probe rejection replaced the current generation")
			}
			if current != nil {
				if err := probeControlGeneration(ctx, current, time.Second); err != nil {
					t.Errorf("replacement cannot carry an authenticated round trip: %v", err)
				}
			}
			if err := probePooledQUIC(ctx, bulk.conn, time.Second); err != nil {
				t.Errorf("bulk sibling cannot carry an authenticated round trip: %v", err)
			}
			if got := c.metrics.Snapshot().PeerProtocolViolations; got != 1 {
				t.Errorf("real protocol violation count = %d, want 1", got)
			}
		})
	}
}
