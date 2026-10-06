package pep

import (
	"context"
	"testing"
	"time"
)

func TestStalledPooledRescueChangesConnectionAndPreservesSibling(t *testing.T) {
	rig := newJoinTestRig(t, TransportAuto, TransportQUIC, 1)
	c := rig.client
	t.Cleanup(c.closeQUICPool)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	initial, err := c.openControlPoolJoinLane(ctx, rig.serverFlow.sessionID, rig.serverFlow.flowID, 1)
	if err != nil {
		t.Fatal(err)
	}
	old := initial.fc.transport().(*controlPoolStreamConn).generation
	sibling, err := c.dialPooledQUICLane(ctx, congestionConfig{kind: defaultCongestion()})
	if err != nil {
		t.Fatal(err)
	}
	defer sibling.Close()
	flow := newGraceTestFlow(t)
	t.Cleanup(flow.closeAll)
	flow.reserveControlLane = true
	if err := flow.addLane(initial); err != nil {
		t.Fatal(err)
	}
	initial.suspected.Store(true)
	flow.setRescueReplacement(initial.id)
	if err := c.runRescueRound(ctx, flow, rig.serverFlow.sessionID, rig.serverFlow.flowID); err != nil {
		t.Fatal(err)
	}
	c.quicMu.Lock()
	fresh := c.quicGeneration
	c.quicMu.Unlock()
	if fresh == old {
		t.Fatal("rescue acknowledged another stream on the same stalled connection")
	}
	if fresh == nil {
		t.Fatal("rescue did not publish a replacement")
	}
	if old.conn.Context().Err() != nil {
		t.Fatal("rotation closed a sibling flow")
	}
	if err := probeControlGeneration(ctx, old, time.Second); err != nil {
		t.Fatalf("sibling path: %v", err)
	}
	// A second flow still on the old generation must borrow the already
	// published successor, not invalidate it and cause another handshake.
	c.drainControlQUICGeneration(old)
	c.quicMu.Lock()
	stillFresh := c.quicGeneration
	c.quicMu.Unlock()
	if stillFresh != fresh {
		t.Fatal("late old-generation rescue drained the successor")
	}
	sibling.Close()
	select {
	case <-old.conn.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("old connection leaked after its final borrower closed")
	}
	if fresh.conn.Context().Err() != nil {
		t.Fatal("old cleanup closed replacement")
	}
}
