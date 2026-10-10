package pathmodel

import (
	"strconv"
	"testing"
	"time"
)

// ACK bursts should not get progressively more expensive as the bandwidth
// window fills. Each report exercises the same four active path members.
func BenchmarkPathModelReportBurst(b *testing.B) {
	for _, reports := range []int{128, 1024, 8192} {
		b.Run(strconv.Itoa(reports), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				m := NewPathModel()
				for i := 0; i < reports; i++ {
					m.Report(Member(i%4+1), Observation{
						Delivered:  float64(1_000_000 + (i*7919)%1_000_000),
						RoundTrip:  100 * time.Millisecond,
						AppLimited: i%7 == 0,
					})
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*reports), "ns/report")
		})
	}
}
