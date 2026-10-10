package pathmodel

import (
	"math/rand/v2"
	"testing"
	"time"
)

func TestBandwidthMaximumMatchesWindowScan(t *testing.T) {
	now := time.Unix(0, 0)
	rng := rand.New(rand.NewPCG(1, 2))
	var measured, capacity bandwidthMaximum
	var history []struct {
		bandwidthSample
		busy bool
	}
	for i := 0; i < 10000; i++ {
		now = now.Add(time.Duration(rng.IntN(50)) * time.Millisecond)
		rate := float64(rng.IntN(1000))
		busy := rng.IntN(4) == 0
		history = append(history, struct {
			bandwidthSample
			busy bool
		}{bandwidthSample{rate: rate, at: now}, busy})
		capacityRate := 0.0
		if busy {
			capacityRate = rate
		}
		got, gotCapacity := measured.observe(now, rate), capacity.observe(now, capacityRate)
		var want, wantCapacity float64
		kept := history[:0]
		for _, sample := range history {
			if now.Sub(sample.at) > bottleneckWindow {
				continue
			}
			kept = append(kept, sample)
			want = max(want, sample.rate)
			if sample.busy {
				wantCapacity = max(wantCapacity, sample.rate)
			}
		}
		history = kept
		if got != want || gotCapacity != wantCapacity {
			t.Fatalf("report %d: measured/capacity = %v/%v, scan = %v/%v", i, got, gotCapacity, want, wantCapacity)
		}
	}
	// An idle read must expire the final sample and release its storage too.
	now = now.Add(bottleneckWindow + time.Nanosecond)
	if measured.maximum(now) != 0 || capacity.maximum(now) != 0 || measured != nil || capacity != nil {
		t.Fatal("idle window retained a bandwidth estimate or its storage")
	}
}

func TestBandwidthMaximumExpiryAndEqualPeaks(t *testing.T) {
	now := time.Unix(0, 0)
	var w bandwidthMaximum
	w.observe(now, 10)
	w.observe(now.Add(time.Second), 10)
	w.observe(now.Add(2*time.Second), 9)
	if got := w.maximum(now.Add(bottleneckWindow + time.Second)); got != 10 {
		t.Fatalf("peak expired at inclusive boundary: got %v", got)
	}
	if got := w.maximum(now.Add(bottleneckWindow + time.Second + time.Nanosecond)); got != 9 {
		t.Fatalf("next lower peak lost after maximum expired: got %v", got)
	}
}
