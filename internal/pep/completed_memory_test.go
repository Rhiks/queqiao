package pep

import (
	"context"
	"net"
	"testing"

	"github.com/bojieli/queqiao/internal/identity"
	"github.com/bojieli/queqiao/internal/protocol"
	"github.com/bojieli/queqiao/internal/stripe"
)

func TestCompletedSessionRetainsOnlyFinalMetadata(t *testing.T) {
	certificate, _ := testCertificate(t)
	server, err := NewServer(ServerConfig{ListenAddr: "127.0.0.1:0", Credentials: certificate})
	if err != nil {
		t.Fatal(err)
	}
	inner, app := net.Pipe()
	defer inner.Close()
	defer app.Close()
	sessionID := [16]byte{1}
	live := newMultipathFlow(context.Background(), inner, sessionID, 7, 1024, protocol.FlagAckDown, protocol.FlagAckUp, nil, nil)
	live.lanes[0] = &mpLane{fc: newFrameConn(inner)}
	live.outstandingChunks = []outstandingChunk{{chunk: &stripe.Chunk{Data: make([]byte, 1<<20)}}}
	live.reserveControlLane = true
	live.finSent.Store(true)
	live.remoteFinSeen.Store(true)
	live.localAbortSent.Store(true)
	live.finSequence.Store(123)
	live.remoteFinSequence.Store(456)
	principal := identity.Principal{AccountID: "owner", DeviceID: "device"}
	original := newServerFlow(live, principal, TransportTCP, 1)
	if !server.registerSession(sessionID, original) {
		t.Fatal("session registration failed")
	}
	server.retainCompletedSession(sessionID, original)
	retained := server.lookupSession(sessionID)
	if retained == nil || !retained.completed.Load() || !samePrincipal(retained.principal, principal) {
		t.Fatal("completed identity was lost")
	}
	if retained == original || retained.flow == live || retained.flow.inner != nil || len(retained.flow.lanes) != 0 || len(retained.flow.outstandingChunks) != 0 || retained.flow.events != nil || retained.flow.scheduler.Load() != nil {
		t.Fatal("tombstone retains live transport or payload state")
	}
	final := retained.flow
	if final.flowID != 7 || final.remoteFinSequence.Load() != 456 || final.finSequence.Load() != 123 || !final.finSent.Load() || !final.remoteFinSeen.Load() || !final.localAbortSent.Load() || !final.reserveControlLane || final.recvAckFlag != protocol.FlagAckUp {
		t.Fatal("final replay metadata changed")
	}
	// Late cleanup of the original runner must not remove its replay record.
	server.unregisterSession(sessionID, original)
	if server.lookupSession(sessionID) != retained {
		t.Fatal("late runner cleanup removed the tombstone")
	}
	// Simultaneous watcher/runner completion is idempotent.
	server.retainCompletedSession(sessionID, original)
	if server.lookupSession(sessionID) != retained {
		t.Fatal("completion replaced its own replay snapshot")
	}
	server.unregisterSession(sessionID, retained)
}

func TestCompletedSessionDoesNotReplaceSuccessor(t *testing.T) {
	certificate, _ := testCertificate(t)
	server, err := NewServer(ServerConfig{ListenAddr: "127.0.0.1:0", Credentials: certificate})
	if err != nil {
		t.Fatal(err)
	}
	id := [16]byte{2}
	old := newServerFlow(&multipathFlow{flowID: 1}, identity.Principal{}, TransportTCP, 1)
	successor := newServerFlow(&multipathFlow{flowID: 2}, identity.Principal{}, TransportTCP, 1)
	server.sessions[id] = successor
	server.retainCompletedSession(id, old)
	if got := server.lookupSession(id); got != successor || got.completed.Load() {
		t.Fatal("late completion replaced a successor")
	}
}
