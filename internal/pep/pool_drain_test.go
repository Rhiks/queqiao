package pep

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestFailedPoolProbeRebuildsWithoutClosingSibling(t *testing.T) {
	rig := newJoinTestRig(t, TransportQUIC, TransportQUIC, 1)
	c := rig.client
	t.Cleanup(c.closeQUICPool)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cc := congestionConfig{kind: defaultCongestion()}
	first, err := c.dialPooledQUICLane(ctx, cc)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	old := first.(*controlPoolStreamConn).generation
	old.probeMu.Lock()
	old.verified = time.Time{}
	old.probeMu.Unlock()
	rejected := errors.New("probe cannot progress")
	c.probeControlGenerationForTest = func(context.Context, *controlQUICGeneration, time.Duration) error { return rejected }
	if _, err = c.dialPooledQUICLane(ctx, cc); !errors.Is(err, rejected) {
		t.Fatalf("failed probe: %v", err)
	}
	next, err := c.dialPooledQUICLane(ctx, cc)
	if err != nil {
		t.Fatalf("new borrow did not recover: %v", err)
	}
	defer next.Close()
	fresh := next.(*controlPoolStreamConn).generation
	if fresh == old {
		t.Fatal("failed probe generation was reused")
	}
	if old.conn.Context().Err() != nil {
		t.Fatal("healthy sibling was closed during rotation")
	}
	// The retained transport still handles a real server round trip.
	if err = probeControlGeneration(ctx, old, time.Second); err != nil {
		t.Fatalf("sibling transport unusable: %v", err)
	}
	first.Close()
	select {
	case <-old.conn.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("drained transport leaked after last borrower closed")
	}
	if fresh.conn.Context().Err() != nil {
		t.Fatal("closing old generation killed replacement")
	}
}

// A real OPEN_OK on the current pool is a fresh round trip for the next
// borrower, even when the last synthetic path probe has aged out.
func TestPooledOpenConfirmationAvoidsRedundantProbe(t *testing.T) {
	rig := newJoinTestRig(t, TransportQUIC, TransportQUIC, 1)
	c := rig.client
	t.Cleanup(c.closeQUICPool)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	destination, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	open, err := c.openFlow(ctx, destination.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer open.fc.Close()
	if !open.openPending {
		t.Fatal("test requires the optimistic OPEN path")
	}
	g := open.outer.(*controlPoolStreamConn).generation
	g.probeMu.Lock()
	g.verified = time.Now().Add(-pooledPathProofMaxAge)
	g.probeMu.Unlock()

	reply, err := open.fc.Read()
	if err != nil {
		t.Fatal(err)
	}
	flow := newGraceTestFlow(t)
	flow.sessionID, flow.flowID = open.sessionID, open.flowID
	flow.requireOpenConfirmation()
	if !flow.deliverInbound(&mpLane{fc: open.fc}, reply) || flow.openConfirmationRequired.Load() {
		t.Fatal("server did not confirm the OPEN")
	}

	var probes atomic.Int32
	c.probeControlGenerationForTest = func(ctx context.Context, g *controlQUICGeneration, budget time.Duration) error {
		probes.Add(1)
		return probeControlGeneration(ctx, g, budget)
	}
	next, err := c.dialPooledQUICLane(ctx, congestionConfig{kind: defaultCongestion()})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if next.(*controlPoolStreamConn).generation != g {
		t.Fatal("working generation replaced")
	}
	if got := probes.Load(); got != 0 {
		t.Fatalf("confirmed OPEN still incurred %d extra probe round trips", got)
	}

	// A failure still makes the next borrower prove the path again. An old
	// OPEN_OK being observed late must not undo that invalidation.
	open.outer.(*controlPoolStreamConn).transportFailed(errors.New("test stream failure"))
	old := newGraceTestFlow(t)
	old.sessionID, old.flowID = open.sessionID, open.flowID
	old.requireOpenConfirmation()
	if !old.deliverInbound(&mpLane{fc: open.fc}, reply) {
		t.Fatal("valid late response rejected")
	}
	verified, err := c.dialPooledQUICLane(ctx, congestionConfig{kind: defaultCongestion()})
	if err != nil {
		t.Fatal(err)
	}
	defer verified.Close()
	if got := probes.Load(); got != 1 {
		t.Fatalf("failure should require a new probe, got %d", got)
	}
}
