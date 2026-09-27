package pep

import (
	"context"
	"github.com/bojieli/queqiao/internal/protocol"
	"net"
	"testing"
)

func TestACKValidationIsAtomicAndProgressRequiresNewCoverage(t *testing.T) {
	for _, name := range []string{"malformed", "beyond-sent", "duplicate", "empty"} {
		t.Run(name, func(t *testing.T) {
			inner, peer := net.Pipe()
			defer inner.Close()
			defer peer.Close()
			f := newMultipathFlow(context.Background(), inner, [16]byte{1}, 7, 1024, protocol.FlagAckUp, protocol.FlagAckDown, nil, nil)
			defer f.closeAll()
			f.noteSent(0, 8)
			lane := &mpLane{id: 9}
			lane.suspected.Store(true)
			f.setRescueReplacement(lane.id)
			frame := protocol.Frame{Header: protocol.Header{Version: protocol.Version, Type: protocol.TypeAck, SessionID: f.sessionID, FlowID: f.flowID, Flags: protocol.FlagAckUp | protocol.FlagAckRanges}}
			switch name {
			case "malformed":
				frame.Header.Sequence = 3
				frame.Payload = []byte{1}
			case "beyond-sent":
				frame.Payload, _ = protocol.EncodeAckRanges([][2]uint64{{4, 9}})
			case "duplicate":
				frame.Payload, _ = protocol.EncodeAckRanges([][2]uint64{{4, 8}})
				if err := f.receiveACK(inboundEvent{frame: frame}); err != nil {
					t.Fatal(err)
				}
			case "empty":
				frame.Payload, _ = protocol.EncodeAckRanges(nil)
			}
			f.lastAckProgressNS.Store(1)
			err := f.receiveACK(inboundEvent{frame: frame, lane: lane})
			if name == "malformed" || name == "beyond-sent" {
				if err == nil {
					t.Fatal("invalid ACK accepted")
				}
				if f.acked != 0 {
					t.Fatal("invalid ACK advanced cumulative point")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if f.lastAckProgressNS.Load() != 1 || !lane.suspected.Load() {
				t.Fatal("non-progress ACK changed health")
			}
			if id, ok := f.rescueReplacement(); !ok || id != lane.id {
				t.Fatal("non-progress ACK cleared recovery hint")
			}
			if name == "empty" {
				frame.Payload, _ = protocol.EncodeAckRanges([][2]uint64{{4, 8}})
				if err := f.receiveACK(inboundEvent{frame: frame, lane: lane}); err != nil {
					t.Fatal(err)
				}
				if f.lastAckProgressNS.Load() == 1 || lane.suspected.Load() {
					t.Fatal("new coverage did not publish progress")
				}
			}
		})
	}
}
