package pep

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bojieli/queqiao/internal/metrics"
	"github.com/bojieli/queqiao/internal/protocol"
)

type cancellableProbeConn struct {
	net.Conn
	key                string
	replies            *bytes.Reader
	reading            chan struct{}
	echoed             chan struct{}
	readOnce, echoOnce sync.Once
	writes             atomic.Int64
}

func (c *cancellableProbeConn) Write(p []byte) (int, error) {
	c.writes.Add(1)
	return len(p), nil
}
func (c *cancellableProbeConn) Read(p []byte) (int, error) {
	c.readOnce.Do(func() { close(c.reading) })
	if c.replies == nil {
		return c.Conn.Read(p)
	}
	n, err := c.replies.Read(p)
	if c.replies.Len() == 0 {
		c.echoOnce.Do(func() { close(c.echoed) })
	}
	return n, err
}
func (c *cancellableProbeConn) CloseWrite() error    { return nil }
func (c *cancellableProbeConn) pathIdentity() string { return c.key }

func TestPrewarmCancellationStopsEveryPhase(t *testing.T) {
	for _, phase := range []string{"before probe", "reading echo", "waiting for measurement"} {
		t.Run(phase, func(t *testing.T) {
			local, remote := net.Pipe()
			defer remote.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			outer := &cancellableProbeConn{Conn: local, key: t.Name(), reading: make(chan struct{}), echoed: make(chan struct{})}
			lane := &authenticatedLane{outer: outer, fc: newFrameConn(outer), kind: TransportQUIC, sessionID: [16]byte{1}}
			if phase != "reading echo" {
				var data bytes.Buffer
				for i := 0; i < pathProbePackets; i++ {
					if err := protocol.WriteFrame(&data, protocol.Frame{Header: protocol.Header{
						Version: protocol.Version, Type: protocol.TypeProbe, SessionID: lane.sessionID,
						FlowID: probeFlowID, Sequence: uint64(i), Class: protocol.ClassNew,
					}, Payload: make([]byte, probePayloadBytes)}); err != nil {
						t.Fatal(err)
					}
				}
				outer.replies = bytes.NewReader(data.Bytes())
			}
			client := &Client{cfg: ClientConfig{Transport: TransportQUIC, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, metrics: metrics.New()}
			client.dialAuthenticatedLaneForTest = func(context.Context, TransportKind) (*authenticatedLane, error) {
				if phase == "before probe" {
					cancel()
				}
				return lane, nil
			}
			done := make(chan struct{})
			go func() { defer close(done); client.prewarmPath(ctx) }()
			defer func() {
				_ = local.Close()
				select {
				case <-done:
				case <-time.After(pathProbeBudget + time.Second):
					t.Error("prewarm did not terminate")
				}
			}()
			switch phase {
			case "reading echo":
				select {
				case <-outer.reading:
				case <-time.After(time.Second):
					t.Fatal("probe never read echo")
				}
			case "waiting for measurement":
				select {
				case <-outer.echoed:
				case <-time.After(time.Second):
					t.Fatal("probe never consumed echo")
				}
			}
			cancel()
			select {
			case <-done:
			case <-time.After(300 * time.Millisecond):
				t.Fatal("canceled prewarm kept waiting for its probe budget")
			}
			if phase == "before probe" && outer.writes.Load() != 0 {
				t.Fatalf("canceled prewarm sent %d unnecessary frames", outer.writes.Load())
			}
			if got := client.metrics.Snapshot().PeerProtocolViolations; got != 0 {
				t.Fatalf("cancellation counted as %d peer violations", got)
			}
		})
	}
}
