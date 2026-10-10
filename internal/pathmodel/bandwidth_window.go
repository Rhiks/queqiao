package pathmodel

import "time"

// bandwidthMaximum keeps only candidates for the exact windowed maximum.
// A newer sample at least as large as an older one outlives and dominates it.
// Each sample enters and leaves once, instead of rescanning ten seconds of
// ACK history on every acknowledgement while holding the shared path lock.
// Callers serialize observations and supply nondecreasing timestamps.
type bandwidthMaximum []bandwidthSample

func (w *bandwidthMaximum) observe(now time.Time, rate float64) float64 {
	w.maximum(now)
	if rate > 0 {
		for len(*w) > 0 && (*w)[len(*w)-1].rate <= rate {
			*w = (*w)[:len(*w)-1]
		}
		*w = append(*w, bandwidthSample{rate: rate, at: now})
	}
	if len(*w) == 0 {
		return 0
	}
	return (*w)[0].rate
}

func (w *bandwidthMaximum) maximum(now time.Time) float64 {
	for len(*w) > 0 && now.Sub((*w)[0].at) > bottleneckWindow {
		*w = (*w)[1:]
	}
	if len(*w) == 0 {
		// Release the backing storage after the path becomes idle.
		*w = nil
		return 0
	}
	return (*w)[0].rate
}
