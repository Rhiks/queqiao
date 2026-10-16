package pep

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bojieli/queqiao/internal/protocol"
)

type endpointReadFailure struct{ net.Conn }

func (*endpointReadFailure) Read([]byte) (int, error) { return 0, os.ErrDeadlineExceeded }

// A destination timeout or policy expiry ends the logical flow, not just its
// lane. The other endpoint must learn that before EOF makes it attempt JOIN
// against a session the server has already removed.
func TestTerminalEndpointFailureReachesPeerWithoutLaneRecovery(t *testing.T) {
	for _, failure := range []string{"read", "write", "idle"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			local, application := net.Pipe()
			defer application.Close()
			inner, destination := net.Pipe()
			defer destination.Close()
			var endpoint net.Conn = inner
			wantLocal := error(os.ErrDeadlineExceeded)
			wantPeer := "flow endpoint I/O timeout"
			switch failure {
			case "read":
				endpoint = &endpointReadFailure{Conn: inner}
			case "write":
				endpoint = &writeFailConn{Conn: inner, err: os.ErrDeadlineExceeded}
			case "idle":
				wantLocal, wantPeer = errFlowIdleTimeout, errFlowIdleTimeout.Error()
			}
			id := [16]byte{11}
			client := newMultipathFlow(ctx, local, id, 9, 1024, protocol.FlagAckUp, protocol.FlagAckDown, nil, nil)
			server := newMultipathFlow(ctx, endpoint, id, 9, 1024, protocol.FlagAckDown, protocol.FlagAckUp, nil, nil)
			defer client.closeAll()
			defer server.closeAll()
			if failure == "idle" {
				server.idleTimeout = 30 * time.Millisecond
			}
			a, b := net.Pipe()
			if err := client.addLane(&mpLane{id: 0, kind: TransportTCP, fc: newFrameConn(a)}); err != nil {
				t.Fatal(err)
			}
			if err := server.addLane(&mpLane{id: 0, kind: TransportTCP, fc: newFrameConn(b)}); err != nil {
				t.Fatal(err)
			}
			clientResult, serverResult := make(chan error, 1), make(chan error, 1)
			go func() { _, err := client.run(ctx); clientResult <- err }()
			go func() { _, err := server.run(ctx); serverResult <- err }()
			if failure == "write" {
				go func() { _, _ = application.Write([]byte("request")) }()
			}
			if err := <-serverResult; !errors.Is(err, wantLocal) {
				t.Fatalf("local cause = %v, want %v", err, wantLocal)
			}
			if err := <-clientResult; err == nil || !strings.Contains(err.Error(), "peer reset flow: "+wantPeer) {
				t.Fatalf("peer lost terminal cause: %v", err)
			}
			if waits := client.replacementWaits.Load(); waits != 0 {
				t.Fatalf("terminal failure started %d lane recovery waits", waits)
			}
		})
	}
}

func TestDestinationFailureStopsRecoveryOverTCPAndPooledQUIC(t *testing.T) {
	for _, kind := range []TransportKind{TransportTCP, TransportQUIC} {
		t.Run(string(kind), func(t *testing.T) {
			certificate, credentials := testCertificate(t)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			tcp, udp := listenTCPAndUDPOnOnePort(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			server, err := NewServer(ServerConfig{
				ListenAddr: tcp.Addr().String(), Credentials: certificate,
				EnableTCP: true, EnableQUIC: true, Logger: logger,
				testDestinationDial: func(context.Context, string) (net.Conn, error) {
					inner, peer := net.Pipe()
					t.Cleanup(func() { _ = peer.Close() })
					return &endpointReadFailure{Conn: inner}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			returned := make(chan error, 2)
			go func() { returned <- server.ServeListener(ctx, tcp) }()
			go func() { returned <- server.ServePacketConn(ctx, udp) }()
			t.Cleanup(func() {
				cancel()
				_ = tcp.Close()
				_ = udp.Close()
				for range 2 {
					select {
					case <-returned:
					case <-time.After(time.Second):
						t.Error("server outlived terminal-failure test")
					}
				}
			})
			client, err := NewClient(ClientConfig{
				ListenAddr: "127.0.0.1:0", RemoteAddr: tcp.Addr().String(),
				Credentials: credentials, Transport: kind, EnableQUICPool: true, Logger: logger,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer client.closeQUICPool()
			opened, err := client.openFlow(ctx, "127.0.0.1:80")
			if err != nil {
				t.Fatal(err)
			}
			inner, app := net.Pipe()
			defer app.Close()
			flow := newMultipathFlow(ctx, inner, opened.sessionID, opened.flowID, 1024,
				protocol.FlagAckUp, protocol.FlagAckDown, nil, nil)
			if opened.openPending {
				flow.requireOpenConfirmation()
			}
			if err := flow.addLane(&mpLane{id: 0, kind: kind, fc: opened.fc}); err != nil {
				t.Fatal(err)
			}
			go client.manageLanes(ctx, flow, opened.sessionID, opened.flowID, kind)
			_, err = flow.run(ctx)
			if err == nil || !strings.Contains(err.Error(), "peer reset flow: flow endpoint I/O timeout") {
				t.Fatalf("destination failure lost over %s: %v", kind, err)
			}
			if flow.replacementWaits.Load() != 0 || flow.replacementAttempts.Load() != 0 {
				t.Fatal("destination failure triggered transport recovery")
			}
		})
	}
}
