package pep

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/bojieli/queqiao/internal/coded"
	"github.com/bojieli/queqiao/internal/pathmodel"
	"github.com/bojieli/queqiao/internal/protocol"
	"github.com/bojieli/queqiao/internal/stripe"
)

// A lost coded response must escape onto the already authenticated stream.
// In particular the server has no client lane manager to consume stallSignal.
func TestCodedSendStallRecoversOnExistingStream(t *testing.T) {
	flow := newStallTestFlow(t, nil)
	flow.stallGrace = 10 * time.Millisecond
	flow.stallScan = time.Millisecond
	model := pathmodel.NewPathModel()
	model.Report(1, pathmodel.Observation{Erasure: .2, BurstFactor: 1, ObservedSamples: 5000, Delivered: 2e6})
	path := coded.New(newPipeCarrier(), coded.Config{Path: model})
	defer path.Close()
	capture := newAckCaptureConn(0, nil)
	defer capture.Close()
	fc := newSplitFrameConn(capture, path)
	fc.setCodingPolicy(flow.prefersCodingOverRetransmission)
	lane := &mpLane{id: 0, kind: TransportQUIC, fc: fc, writeQ: make(chan laneFrame, 2), writeDone: make(chan struct{})}
	lane.ready.Store(true)
	flow.lanes[0] = lane
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	now := time.Now()
	sched := stripe.New(bytes.NewReader([]byte("response")), stripe.Config{
		Reliable:        func(uint64) bool { return !fc.codesData() },
		RetransmitAfter: func() time.Duration { return time.Millisecond }, Now: func() time.Time { return now },
	})
	defer sched.Close()
	flow.scheduler.Store(sched)
	first, err := sched.Next(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.Reliable {
		t.Fatal("fixture must begin on coded datagrams")
	}
	if err = flow.sendChunk(ctx, lane, first, true); err != nil {
		t.Fatal(err)
	}
	sent := <-lane.writeQ
	// The datagram was accepted by the carrier but erased before delivery.
	sent.onWritten()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { flow.stallWatchdog(stop); close(done) }()
	defer func() { close(stop); <-done }()
	select {
	case <-flow.stallSignals():
	case <-ctx.Done():
		t.Fatal("no stall detected")
	}
	now = now.Add(time.Second)
	if sched.ReissueExpired() != 1 {
		t.Fatal("lost response was not reissued")
	}
	retry, err := sched.Next(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !retry.Reliable {
		t.Fatal("stalled response keeps retrying unreliable datagrams; no stream escape")
	}
	if err = flow.sendChunk(ctx, lane, retry, true); err != nil {
		t.Fatal(err)
	}
	queued := <-lane.writeQ
	if err = fc.writeContextMode(ctx, queued.frame, queued.forceReliable); err != nil {
		t.Fatal(err)
	}
	queued.onWritten()
	frame := waitAckFrame(t, capture)
	if lane.closed.Load() {
		t.Fatal("recovery closed the existing lane")
	}
	if frame.Header.Type != protocol.TypeData || string(frame.Payload) != "response" {
		t.Fatal("response not delivered on stream")
	}

}
