package congestion

import (
	"testing"
	"time"

	quiccongestion "github.com/apernet/quic-go/congestion"
	"github.com/apernet/quic-go/monotime"
	"github.com/bojieli/queqiao/internal/pathmodel"
)

type deliveryRecordingModel struct {
	state pathmodel.State
	last  pathmodel.Observation
}

func (m *deliveryRecordingModel) Current() pathmodel.State { return m.state }
func (m *deliveryRecordingModel) Report(_ pathmodel.Member, o pathmodel.Observation) pathmodel.State {
	m.last = o
	return m.state
}

func TestSharedPathSeedIsDeliveredBandwidth(t *testing.T) {
	m := &deliveryRecordingModel{state: pathmodel.State{
		Seed: 1_000_000, Erasure: 0.42, RoundTrip: 200 * time.Millisecond,
	}}
	e := NewErasureSenderOn(1200, m)
	if got := e.inner.estimator.estimate(); got != uint64(m.state.Seed) {
		t.Fatalf("seed inflated to %d, want delivered rate %.0f before compensation", got, m.state.Seed)
	}
	if e.arrivalRate() > 0.59 {
		t.Fatal("joining sender lost the measured erasure compensation")
	}
}

func TestSharedPathReportsAcknowledgedDeliveryNotInheritedPacing(t *testing.T) {
	m := &deliveryRecordingModel{state: pathmodel.State{
		Seed: 1_000_000, RoundTrip: 200 * time.Millisecond,
	}}
	e := NewErasureSenderOn(1200, m)
	now := monotime.Now()
	e.OnCongestionEventEx(0, now, nil, nil)
	if m.last.Delivered != 0 {
		t.Fatalf("unmeasured sender reported its inherited rate as delivery: %.0f", m.last.Delivered)
	}
	e.inner.estimator.markAppLimited()
	e.OnPacketSent(now, 0, 1, 1200, true)
	e.OnCongestionEventEx(1200, now.Add(100*time.Millisecond), []quiccongestion.AckedPacketInfo{
		{PacketNumber: 1, BytesAcked: 1200},
	}, nil)
	// The next report publishes the completed ACK event. The inherited model
	// remains useful for pacing, but cannot count as another lane's evidence.
	e.OnCongestionEventEx(0, now.Add(101*time.Millisecond), nil, nil)
	if got := m.last.Delivered; got != 12_000 {
		t.Fatalf("reported delivery %.0f, want the acknowledged 1200 bytes / 100ms", got)
	}
	if e.inner.estimator.estimate() != uint64(m.state.Seed) {
		t.Fatal("small app-limited exchange discarded the useful pacing seed")
	}
}

func TestSmallAppLimitedExchangesDoNotCapSharedPath(t *testing.T) {
	m := pathmodel.NewPathModel()
	now := monotime.Now()
	for i := 0; i < 2; i++ {
		e := NewErasureSenderOn(1200, m)
		e.inner.estimator.markAppLimited()
		e.OnPacketSent(now, 0, 1, 1200, true)
		e.OnCongestionEventEx(1200, now.Add(100*time.Millisecond), []quiccongestion.AckedPacketInfo{
			{PacketNumber: 1, BytesAcked: 1200},
		}, nil)
		e.OnCongestionEventEx(0, now.Add(101*time.Millisecond), nil, nil)
		if got := e.Share(); got != 0 {
			t.Fatalf("small exchange created a shared capacity ceiling of %.0f bytes/s", got)
		}
	}
	state := m.Current()
	if state.Share != 0 {
		t.Fatalf("new transfer inherited a %.0f bytes/s ceiling from small exchanges", state.Share)
	}
	if state.Seed <= 0 {
		t.Fatal("useful measured delivery was discarded instead of remaining a seed")
	}
}

func TestIdleMembersDoNotDivideSaturatedPathShare(t *testing.T) {
	m := pathmodel.NewPathModel()
	m.Report(1, pathmodel.Observation{Delivered: 1000, AppLimited: true})
	first := m.Report(2, pathmodel.Observation{Delivered: 1_000_000})
	if first.Share != 0 {
		t.Fatalf("single busy sender was capped at %.0f by an idle sibling", first.Share)
	}
	second := m.Report(3, pathmodel.Observation{Delivered: 1_000_000})
	if second.Share < 1_200_000 || second.Share > 1_300_000 {
		t.Fatalf("two busy senders received share %.0f, want an even split with probe headroom", second.Share)
	}
}
