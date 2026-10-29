package congestion

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"

	quiccongestion "github.com/apernet/quic-go/congestion"
	"github.com/apernet/quic-go/monotime"
	"github.com/bojieli/queqiao/internal/lossmodel"
)

// ACK/loss batches may be unordered and reuse packet numbers across QUIC
// spaces. Preserve the previous callback's statistics, including those ties.
func TestErasureEventsPreserveExplicitOutcomeStatistics(t *testing.T) {
	rng := rand.New(rand.NewSource(35))
	e := NewErasureSender(1200)
	reference := lossmodel.New(lossmodel.Config{ReorderTolerance: 32})
	for batch := 0; batch < 1000; batch++ {
		acked := make([]quiccongestion.AckedPacketInfo, rng.Intn(128))
		lost := make([]quiccongestion.LostPacketInfo, rng.Intn(32))
		var expected []packetOutcome
		for i := range acked {
			acked[i].PacketNumber = quiccongestion.PacketNumber(rng.Intn(65) - 1)
			if acked[i].PacketNumber >= 0 {
				expected = append(expected, packetOutcome{number: acked[i].PacketNumber, arrived: true})
			}
		}
		for i := range lost {
			lost[i].PacketNumber = quiccongestion.PacketNumber(rng.Intn(65) - 1)
			if lost[i].PacketNumber >= 0 {
				expected = append(expected, packetOutcome{number: lost[i].PacketNumber})
			}
		}
		sort.Slice(expected, func(i, j int) bool { return expected[i].number < expected[j].number })
		for _, outcome := range expected {
			reference.ObserveOutcome(outcome.arrived)
		}
		e.OnCongestionEventEx(0, monotime.Now(), acked, lost)
		if got, want := e.estimator.Snapshot(), reference.Snapshot(); !reflect.DeepEqual(got, want) {
			t.Fatalf("batch %d changed channel statistics: got %+v, want %+v", batch, got, want)
		}
	}
}
