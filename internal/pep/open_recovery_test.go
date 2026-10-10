package pep

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/bojieli/queqiao/internal/protocol"
)

// A destination lookup can outlast the DATA stall threshold. The optimistic
// SOCKS reply still lets the application write, but no session exists at the
// gateway until that lookup/dial completes. A rescue JOIN in that interval
// gets unknown_session and used to close the application before OPEN_OK.
func TestOptimisticOpenSurvivesSlowDestination(t *testing.T) {
	destination, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = destination.Close() })
	go echoDestination(destination)
	certificate, credentials := testCertificate(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{
		ListenAddr:  packet.LocalAddr().String(),
		Credentials: certificate, EnableQUIC: true, Logger: logger,
		DestinationPolicy: DestinationPolicy{AllowPrivate: true},
		testDestinationDial: func(ctx context.Context, address string) (net.Conn, error) {
			timer := time.NewTimer(1500 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return (DestinationPolicy{AllowPrivate: true}).DialContext(ctx, address)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(ClientConfig{
		ListenAddr: listener.Addr().String(),
		RemoteAddr: packet.LocalAddr().String(), Credentials: credentials,
		Transport: TransportQUIC, EnableQUICPool: true, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 2)
	go func() { returned <- server.ServePacketConn(ctx, packet) }()
	go func() { returned <- client.ServeListener(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		_ = packet.Close()
		_ = listener.Close()
		for range 2 {
			select {
			case <-returned:
			case <-time.After(3 * time.Second):
				t.Error("proxy outlived test cancellation")
			}
		}
	})
	conn := socksDial(t, listener.Addr().String(), destination, 5*time.Second)
	defer conn.Close()
	payload := bytes.Repeat([]byte("request"), 256)
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("pending OPEN was interrupted: %v (rescue attempts %d)", err, client.metrics.Snapshot().LaneRescueAttempts)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("request/reply bytes changed")
	}
	if attempts := client.metrics.Snapshot().LaneRescueAttempts; attempts != 0 {
		t.Fatalf("opening session triggered %d premature rescue JOINs", attempts)
	}
}

func TestPendingOpenWaitStopsWithItsOwner(t *testing.T) {
	for _, end := range []string{"confirmation", "context", "flow", "watchdog"} {
		t.Run(end, func(t *testing.T) {
			flow := newStallTestFlow(t, nil)
			flow.requireOpenConfirmation()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stop := make(chan struct{})
			done := make(chan bool, 1)
			go func() { done <- flow.waitForOpenConfirmation(ctx, stop) }()
			switch end {
			case "confirmation":
				if !flow.deliverInbound(nil, protocol.Frame{Header: protocol.Header{
					Type: protocol.TypeOpenOK, SessionID: flow.sessionID, FlowID: flow.flowID,
				}}) {
					t.Fatal("valid OPEN acknowledgement was rejected")
				}
			case "context":
				cancel()
			case "flow":
				flow.signalDone()
			case "watchdog":
				close(stop)
			}
			select {
			case confirmed := <-done:
				if confirmed != (end == "confirmation") {
					t.Fatalf("wait result = %v after %s", confirmed, end)
				}
			case <-time.After(time.Second):
				t.Fatalf("OPEN wait outlived %s", end)
			}
		})
	}
}
