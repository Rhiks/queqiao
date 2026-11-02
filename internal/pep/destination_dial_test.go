package pep

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestDestinationDialDoesNotLetFirstAddressExhaustDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	winner, peer := net.Pipe()
	defer winner.Close()
	defer peer.Close()
	started := time.Now()
	conn, err := dialDestinationCandidates(ctx, []string{"first", "second"}, func(ctx context.Context, address string) (net.Conn, error) {
		if address == "first" {
			<-ctx.Done() // A route which silently drops SYNs, rather than refusing them.
			return nil, ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return winner, nil
	})
	if err != nil || conn != winner {
		t.Fatalf("reachable second address starved behind failed first address: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 800*time.Millisecond {
		t.Fatalf("fallback took %v of a one-second total budget", elapsed)
	}
	t.Logf("reachable second address connected in %v", time.Since(started))
}

func TestDestinationDialBoundsConcurrencyAndReachesLaterAddresses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	winner, peer := net.Pipe()
	defer winner.Close()
	defer peer.Close()
	var active, peak atomic.Int32
	conn, err := dialDestinationCandidates(ctx, []string{"first", "second", "third", "fourth"}, func(ctx context.Context, address string) (net.Conn, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		if address == "third" {
			return winner, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil || conn != winner || peak.Load() > 2 {
		t.Fatalf("later candidate unreachable or socket bound exceeded: conn=%v err=%v peak=%d", conn, err, peak.Load())
	}
}

type destinationObservedConn struct {
	net.Conn
	closed chan struct{}
	once   sync.Once
}

func (c *destinationObservedConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

func TestDestinationDialClosesLateSuccessfulLoser(t *testing.T) {
	winner, peer := net.Pipe()
	defer winner.Close()
	defer peer.Close()
	l, lp := net.Pipe()
	defer lp.Close()
	loser := &destinationObservedConn{Conn: l, closed: make(chan struct{})}
	defer loser.Close()
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := dialDestinationCandidates(ctx, []string{"first", "second"}, func(_ context.Context, address string) (net.Conn, error) {
		if address == "first" {
			<-release // Completion racing with cancellation must close this socket.
			return loser, nil
		}
		return winner, nil
	})
	close(release)
	if err != nil || conn != winner {
		t.Fatalf("fallback failed: %v", err)
	}
	select {
	case <-loser.closed:
	case <-time.After(time.Second):
		t.Fatal("successful losing connection leaked")
	}
}

func TestDestinationDialDoesNotSpeculateAfterImmediateSuccess(t *testing.T) {
	winner, peer := net.Pipe()
	defer winner.Close()
	defer peer.Close()
	var calls atomic.Int32
	conn, err := dialDestinationCandidates(context.Background(), []string{"first", "second"}, func(context.Context, string) (net.Conn, error) {
		calls.Add(1)
		return winner, nil
	})
	if err != nil || conn != winner || calls.Load() != 1 {
		t.Fatalf("healthy first address dialed %d times: %v", calls.Load(), err)
	}
}

func TestDestinationDialHonorsCancellationAndPrivatePolicy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := dialDestinationCandidates(ctx, []string{"first", "second"}, func(context.Context, string) (net.Conn, error) {
		t.Error("dial attempted after cancellation")
		return nil, errors.New("unexpected dial")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled dial: %v", err)
	}
	if conn, err := (DestinationPolicy{}).DialContext(context.Background(), "127.0.0.1:443"); err == nil || conn != nil {
		t.Fatal("private TCP destination accepted")
	}
}

// A resolver can group several addresses from a broken family before a
// reachable alternate. Both workers must not wait on that same family.
func TestDestinationDialRacesAddressFamilies(t *testing.T) {
	for _, addresses := range [][]string{
		{"[2001:db8::1]:443", "[2001:db8::2]:443", "192.0.2.1:443"},
		{"192.0.2.1:443", "192.0.2.2:443", "[2001:db8::1]:443"},
	} {
		t.Run(addresses[0], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			winner, peer := net.Pipe()
			defer winner.Close()
			defer peer.Close()
			started := time.Now()
			conn, err := dialDestinationCandidates(ctx, addresses, func(ctx context.Context, address string) (net.Conn, error) {
				if address == addresses[2] {
					return winner, nil
				}
				<-ctx.Done()
				return nil, ctx.Err()
			})
			if err != nil || conn != winner {
				t.Fatalf("alternate family failed: %v", err)
			}
			if elapsed := time.Since(started); elapsed > 700*time.Millisecond {
				t.Fatalf("healthy alternate family waited behind blackholed addresses for %v", elapsed)
			}
			t.Logf("alternate connected in %v", time.Since(started))
		})
	}
}

func TestDestinationFailureReasonDoesNotDiscloseTarget(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{&net.DNSError{Err: "private resolver text", Name: "private.example", IsTimeout: true}, "dns_timeout"},
		{fmt.Errorf("resolve: %w", &net.DNSError{Err: "no such host", Name: "private.example", IsNotFound: true}), "dns_not_found"},
		{&net.DNSError{Err: "resolver failure", Name: "private.example"}, "dns_error"},
		{errDestinationNotPublic, "address_not_public"},
		{errDestinationNoAddresses, "no_addresses"},
		{context.Canceled, "cancelled"},
		{&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, "connection_refused"},
		{syscall.ENETUNREACH, "network_unreachable"},
		{fmt.Errorf("dial: %w", os.ErrDeadlineExceeded), "connect_timeout"},
		{errors.New("failure for private.example"), "connect_error"},
	} {
		if got := destinationFailureReason(test.err); got != test.want {
			t.Errorf("reason = %s, want %s", got, test.want)
		}
	}
}
