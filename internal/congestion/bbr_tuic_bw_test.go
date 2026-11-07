package congestion

import (
	"testing"
	"time"

	quiccongestion "github.com/apernet/quic-go/congestion"
	"github.com/apernet/quic-go/monotime"
)

// removeObsolete used to scan everything in flight on every congestion event.
// At 300 Mbit/s over a 200ms round trip that is about six thousand entries per
// acknowledgement, and a CPU profile of a 294 Mbit/s transfer attributed 13% of
// all time to that one loop.
func BenchmarkRemoveObsolete(b *testing.B) {
	const inFlight = 6000
	for b.Loop() {
		b.StopTimer()
		e := newTUICBandwidthEstimator()
		for i := range inFlight {
			e.packetStates[quiccongestion.PacketNumber(i)] = tuicPacketState{}
		}
		e.lowestState, e.lowestKnown = 0, true
		b.StartTimer()
		// QUIC acknowledges roughly every other packet, so a window is retired
		// over hundreds of congestion events rather than a handful. The scan
		// costs the whole window on each of them; the walk costs the window
		// once, however many events it is spread across.
		const acks = 300
		for k := 1; k <= acks; k++ {
			e.removeObsolete(quiccongestion.PacketNumber(k * inFlight / acks))
		}
	}
}

// The walk and the scan have to agree, including when a bulk prune has emptied
// the map behind the watermark's back.
func TestRemoveObsoleteMatchesAScan(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*tuicBandwidthEstimator)
		cut     quiccongestion.PacketNumber
	}{
		{"contiguous", func(e *tuicBandwidthEstimator) {}, 500},
		{"nothing to drop", func(e *tuicBandwidthEstimator) {}, 0},
		{"everything", func(e *tuicBandwidthEstimator) {}, 1000},
		{"past the end", func(e *tuicBandwidthEstimator) {}, 5000},
		{"after a bulk prune", func(e *tuicBandwidthEstimator) {
			for i := range 900 {
				delete(e.packetStates, quiccongestion.PacketNumber(i))
			}
		}, 950},
	} {
		t.Run(tc.name, func(t *testing.T) {
			build := func() *tuicBandwidthEstimator {
				e := newTUICBandwidthEstimator()
				for i := range 1000 {
					e.packetStates[quiccongestion.PacketNumber(i)] = tuicPacketState{}
				}
				e.lowestState, e.lowestKnown = 0, true
				tc.prepare(&e)
				return &e
			}
			fast := build()
			fast.removeObsolete(tc.cut)

			slow := build()
			for number := range slow.packetStates {
				if number < tc.cut {
					delete(slow.packetStates, number)
				}
			}
			if len(fast.packetStates) != len(slow.packetStates) {
				t.Fatalf("walk left %d states, scan left %d", len(fast.packetStates), len(slow.packetStates))
			}
			for number := range slow.packetStates {
				if _, ok := fast.packetStates[number]; !ok {
					t.Fatalf("walk dropped packet %d that the scan kept", number)
				}
			}
		})
	}
}

func TestObsoletePacketNumberRetainsReorderWindowAndLossStates(t *testing.T) {
	if _, ok := obsoletePacketNumber(nil, []quiccongestion.LostPacketInfo{{PacketNumber: 50, BytesLost: 1200}}); ok {
		t.Fatal("loss-only event must not evict packet states reserved for spurious-loss detection")
	}
	cut, ok := obsoletePacketNumber([]quiccongestion.AckedPacketInfo{{PacketNumber: 300, BytesAcked: 1200}}, nil)
	if !ok || cut != 300-tuicObsoletePacketWindow {
		t.Fatalf("obsolete cut = (%d, %v), want (%d, true)", cut, ok, 300-tuicObsoletePacketWindow)
	}
}

func TestOnAckBatchStateMissPreservesCumulativeTotalAcked(t *testing.T) {
	e := newTUICBandwidthEstimator()
	t0 := monotime.Now()
	e.onSentPacket(t0, 1, 1200, 0, true)
	e.onAckBatch(t0.Add(20*time.Millisecond), []quiccongestion.AckedPacketInfo{
		{PacketNumber: 1, BytesAcked: 1200, ReceivedTime: t0.Add(20 * time.Millisecond)},
	}, 1)
	e.endAcks()

	e.onSentPacket(t0.Add(25*time.Millisecond), 2, 1200, 0, true)
	e.onSentPacket(t0.Add(26*time.Millisecond), 3, 1200, 1200, true)
	// Simulate premature eviction of packet 2 while packet 3 remains tracked.
	delete(e.packetStates, 2)

	sample := e.onAckBatch(t0.Add(45*time.Millisecond), []quiccongestion.AckedPacketInfo{
		{PacketNumber: 2, BytesAcked: 1200, ReceivedTime: t0.Add(45 * time.Millisecond)},
		{PacketNumber: 3, BytesAcked: 1200, ReceivedTime: t0.Add(45 * time.Millisecond)},
	}, 2)
	if got := e.bytesAckedThisWindow(); got != 2400 {
		t.Fatalf("bytesAckedThisWindow = %d, want 2400 even when one packet state missed", got)
	}
	if e.stateMisses != 1 {
		t.Fatalf("stateMisses = %d, want 1", e.stateMisses)
	}
	if !sample.hasSample || sample.maxBandwidth == 0 {
		t.Fatalf("expected valid bandwidth sample for packet 3, got %+v", sample)
	}
}

func TestRemoveObsoleteIgnoresOutOfOrderLowerWatermarkAndPruneUpdatesLowest(t *testing.T) {
	e := newTUICBandwidthEstimator()
	for i := range tuicMaxSendStates {
		e.packetStates[quiccongestion.PacketNumber(i)] = tuicPacketState{}
	}
	e.lowestState, e.lowestKnown = 0, true
	e.pruneStates()
	wantLowest := quiccongestion.PacketNumber(tuicMaxSendStates / 4)
	if !e.lowestKnown || e.lowestState != wantLowest {
		t.Fatalf("lowest after pruneStates = (%d, %v), want (%d, true)", e.lowestState, e.lowestKnown, wantLowest)
	}
	// A stale/out-of-order cut below lowestState must not regress lowestState.
	e.removeObsolete(wantLowest - 10)
	if e.lowestState != wantLowest {
		t.Fatalf("lowestState regressed to %d, want %d", e.lowestState, wantLowest)
	}
}
