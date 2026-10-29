package congestion

import (
	"fmt"
	"testing"
	"time"

	quiccongestion "github.com/apernet/quic-go/congestion"
	"github.com/apernet/quic-go/monotime"
)

// Include the entire callback, rather than timing an isolated sort: it owns
// loss estimation and BBR updates as well as ordering explicit packet fates.
func BenchmarkErasureCongestionEvent(b *testing.B) {
	for _, count := range []int{8, 64, 256} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			e := NewErasureSender(1200)
			acked := make([]quiccongestion.AckedPacketInfo, count)
			lost := make([]quiccongestion.LostPacketInfo, count/4)
			for i := range acked {
				acked[i].PacketNumber = quiccongestion.PacketNumber(i * 2)
			}
			for i := range lost {
				lost[i].PacketNumber = quiccongestion.PacketNumber(i*8 + 1)
			}
			now := monotime.Now()
			e.OnCongestionEventEx(0, now, acked, lost)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				now = now.Add(time.Millisecond)
				e.OnCongestionEventEx(0, now, acked, lost)
			}
		})
	}
}
