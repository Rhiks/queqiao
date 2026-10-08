package pep

import (
	"context"
	"testing"
	"time"
)

func startStallManager(t *testing.T) (*Client, *multipathFlow) {
	t.Helper()
	rig := newJoinTestRig(t, TransportAuto, TransportQUIC, 1)
	c := rig.client
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(c.closeQUICPool)
	lane, err := c.openControlPoolJoinLane(ctx, rig.serverFlow.sessionID, rig.serverFlow.flowID, 1)
	if err != nil {
		t.Fatal(err)
	}
	flow := newGraceTestFlow(t)
	flow.sessionID, flow.flowID = rig.serverFlow.sessionID, rig.serverFlow.flowID
	flow.reserveControlLane = true
	t.Cleanup(flow.closeAll)
	if err := flow.addLane(lane); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		c.manageQUICLanes(ctx, flow, flow.sessionID, flow.flowID)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("lane manager outlived cancellation")
		}
	})
	return c, flow
}

// A watchdog notification can wait behind another manager operation. Delivery
// may recover before the notification is handled; a queued suspicion must not
// replace the now-working connection.
func TestLaneManagerDropsRecoveredQueuedStall(t *testing.T) {
	c, flow := startStallManager(t)
	flow.noteSent(0, 512)
	flow.suspectDataLanes()
	if err := flow.acknowledgeReplay(512, false); err != nil {
		t.Fatal(err)
	}
	c.quicMu.Lock()
	generation := c.quicGeneration
	c.quicMu.Unlock()
	flow.stallSignal <- struct{}{}
	time.Sleep(150 * time.Millisecond)
	if attempts := c.metrics.Snapshot().LaneRescueAttempts; attempts != 0 {
		t.Fatalf("recovered queued stall started %d rescue attempts", attempts)
	}
	c.quicMu.Lock()
	unchanged := c.quicGeneration == generation
	c.quicMu.Unlock()
	if !unchanged {
		t.Fatal("recovered queued stall rotated the working pool")
	}
}

func requestStallRescue(flow *multipathFlow) {
	flow.suspectDataLanes()
	select {
	case flow.stallSignal <- struct{}{}:
	default:
	}
}

func waitRescueWins(t *testing.T, c *Client, want uint64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.metrics.Snapshot().LaneRescueWins[0] >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("rescue wins = %d, want %d", c.metrics.Snapshot().LaneRescueWins[0], want)
}

// JOIN success proves only that a lane opened. If application delivery remains
// stuck, repeated successful JOINs must retain the same exponential backoff as
// failed JOINs. Real ACK progress, even partial progress, restores prompt rescue.
func TestSuccessfulStallRescuesBackOffUntilDataProgress(t *testing.T) {
	c, flow := startStallManager(t)
	flow.stallGrace = 20 * time.Millisecond
	flow.noteSent(0, 512)
	requestStallRescue(flow)
	waitRescueWins(t, c, 1)
	time.Sleep(1100 * time.Millisecond)
	requestStallRescue(flow)
	waitRescueWins(t, c, 2)
	time.Sleep(1100 * time.Millisecond)
	requestStallRescue(flow)
	time.Sleep(150 * time.Millisecond)
	if wins := c.metrics.Snapshot().LaneRescueWins[0]; wins != 2 {
		t.Fatalf("JOIN without data progress reset the backoff: %d rescue wins, want 2", wins)
	}
	if err := flow.acknowledgeReplay(64, false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	requestStallRescue(flow)
	waitRescueWins(t, c, 3)
}
