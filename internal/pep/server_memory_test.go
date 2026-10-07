package pep

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bojieli/queqiao/internal/stripe"
)

func TestServerMemoryBudgetsCoverConcurrentFlows(t *testing.T) {
	for _, transport := range []TransportKind{TransportTCP, TransportQUIC} {
		t.Run(string(transport), func(t *testing.T) {
			destination, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer destination.Close()
			go echoDestination(destination)
			certificate, credentials := testCertificate(t)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			server, err := NewServer(ServerConfig{
				ListenAddr: "127.0.0.1:0", Credentials: certificate, Logger: logger,
				DestinationPolicy: DestinationPolicy{AllowPrivate: true}, ChunkSize: 16 << 10,
				MemoryLimits: &MemoryLimits{SendBudgetBytes: 64 << 10, ReceiveBudgetBytes: 4 << 20, MaxFlowSendBytes: 64 << 10, MaxFlowReceiveBytes: 1 << 20},
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			services := make(chan error, 2)
			var remote string
			if transport == TransportTCP {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				remote = listener.Addr().String()
				go func() { services <- server.ServeListener(ctx, listener) }()
			} else {
				socket, err := net.ListenPacket("udp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				remote = socket.LocalAddr().String()
				go func() { services <- server.ServePacketConn(ctx, socket) }()
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			client, err := NewClient(ClientConfig{ListenAddr: listener.Addr().String(), RemoteAddr: remote, Credentials: credentials, Transport: transport, EnableQUICPool: true, Logger: logger})
			if err != nil {
				t.Fatal(err)
			}
			go func() { services <- client.ServeListener(ctx, listener) }()
			var wg sync.WaitGroup
			for i := 0; i < 4; i++ {
				conn := dialTestSOCKS(t, listener.Addr().String(), destination.Addr().String())
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer conn.Close()
					conn.SetDeadline(time.Now().Add(10 * time.Second))
					payload := bytes.Repeat([]byte("budget-preserves-content-"), 16384)
					if _, err := conn.Write(payload); err != nil {
						t.Error(err)
						return
					}
					if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
						t.Error(err)
						return
					}
					got, err := io.ReadAll(conn)
					if err != nil {
						t.Error(err)
						return
					}
					if !bytes.Equal(got, payload) {
						t.Errorf("echo content differs: got %d, want %d", len(got), len(payload))
					}
				}()
			}
			wg.Wait()
			cancel()
			for range 2 {
				select {
				case err := <-services:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("service shutdown blocked")
				}
			}
			stats := server.MemoryStats()
			if stats.Send.Used != 0 || stats.Receive.Used != 0 || stats.Send.Peak == 0 || stats.Receive.Peak == 0 || stats.Send.Peak > stats.Send.Capacity || stats.Receive.Peak > stats.Receive.Capacity {
				t.Fatalf("invalid retained-payload accounting after concurrent round trips: %+v", stats)
			}
		})
	}
}

func TestServerDefaultsAndMetricsExposeSharedMemory(t *testing.T) {
	certificate, _ := testCertificate(t)
	server, err := NewServer(ServerConfig{ListenAddr: "127.0.0.1:0", Credentials: certificate})
	if err != nil {
		t.Fatal(err)
	}
	stats := server.MemoryStats()
	if stats.Send.Capacity != 128<<20 || stats.Receive.Capacity != 128<<20 {
		t.Fatalf("server defaults = %+v", stats)
	}
	if !server.sendMemory.TryAcquire(123) {
		t.Fatal("memory reservation failed")
	}
	defer server.sendMemory.Release(123)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(response.Body.String(), "queqiao_payload_memory_used_bytes{direction=\"send\"} 123\n") || !strings.Contains(response.Body.String(), "queqiao_active_flows ") {
		t.Fatal("metrics omit memory or existing counters")
	}
	invalid := httptest.NewRecorder()
	server.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/metrics", nil))
	if invalid.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST returned %d", invalid.Code)
	}
}

func TestAcknowledgedChunksReleaseBackingArrayReferences(t *testing.T) {
	flow := &multipathFlow{ackTrack: newAckTracker()}
	chunk := &stripe.Chunk{Offset: 0, Data: make([]byte, 32<<10)}
	flow.trackChunk(1, chunk)
	scheduler := stripe.New(bytes.NewReader(nil), stripe.Config{})
	defer scheduler.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); flow.watchChunkCompletion(ctx, scheduler) }()
	defer func() { cancel(); <-done }()
	flow.ackTrack.Add([][2]uint64{{0, chunk.End()}})
	deadline := time.Now().Add(time.Second)
	for {
		flow.chunkMu.Lock()
		count := len(flow.outstandingChunks)
		retained := false
		for _, entry := range flow.outstandingChunks[:cap(flow.outstandingChunks)] {
			retained = retained || entry.chunk != nil
		}
		flow.chunkMu.Unlock()
		if count == 0 {
			if retained {
				t.Fatal("ACK released accounting but backing array still retains payload")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ACK completion did not progress")
		}
		time.Sleep(time.Millisecond)
	}
}
