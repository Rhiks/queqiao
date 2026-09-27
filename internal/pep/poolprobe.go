package pep

import (
	"context"
	"errors"
	"time"

	"github.com/bojieli/queqiao/internal/protocol"
	"github.com/bojieli/queqiao/internal/session"
)

// Borrowers share one round-trip check when the last real proof is stale.
// Failure drains this generation: new borrowers get a fresh connection,
// while existing siblings keep their transport until they finish.
type controlPathProbe struct {
	done chan struct{}
	err  error
}

func (c *Client) verifyControlPath(ctx context.Context, g *controlQUICGeneration) error {
	g.probeMu.Lock()
	if time.Since(g.verified) < uplinkPollInterval {
		g.probeMu.Unlock()
		return nil
	}
	pending := g.probe
	if pending == nil {
		pending = &controlPathProbe{done: make(chan struct{})}
		g.probe = pending
		go func() {
			probeCtx, cancel := context.WithTimeout(g.conn.Context(), c.cfg.HandshakeTimeout)
			defer cancel()
			probe := probeControlGeneration
			if c.probeControlGenerationForTest != nil {
				probe = c.probeControlGenerationForTest
			}
			pending.err = probe(probeCtx, g, c.cfg.HandshakeTimeout)
			if pending.err != nil {
				c.drainControlQUICGeneration(g)
			}
			g.probeMu.Lock()
			if pending.err == nil {
				g.verified = time.Now()
			}
			g.probe = nil
			close(pending.done)
			g.probeMu.Unlock()
		}()
	}
	g.probeMu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-pending.done:
		return pending.err
	}
}

func probeControlGeneration(ctx context.Context, g *controlQUICGeneration, budget time.Duration) error {
	stream, err := g.conn.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	outer := &quicStreamConn{stream: stream, conn: g.conn, closeConn: false}
	defer outer.Close()
	_, finish := bindHandshake(ctx, outer, budget)
	defer finish()
	id, err := session.NewSessionID()
	if err != nil {
		return err
	}
	frame := protocol.Frame{Header: protocol.Header{Version: protocol.Version, Type: protocol.TypeProbe, SessionID: id, Class: protocol.ClassNew}, Payload: []byte{1}}
	fc := newFrameConn(outer)
	if err = fc.Write(frame); err != nil {
		return err
	}
	response, err := fc.Read()
	if err != nil {
		return err
	}
	if response.Header.Type != frame.Header.Type || response.Header.SessionID != id || response.Header.FlowID != 0 || response.Header.Sequence != 0 || response.Header.Flags != 0 || len(response.Payload) != 1 || response.Payload[0] != 1 {
		return errors.New("invalid pooled path probe echo")
	}
	return finish()
}

// Detach before marking the generation draining, under the same pool lock
// used to reserve borrowers. No future acquire can revive it.
func (c *Client) drainControlQUICGeneration(g *controlQUICGeneration) {
	c.quicMu.Lock()
	if c.quicGeneration == g {
		c.quicGeneration = nil
	}
	g.borrowMu.Lock()
	g.draining = true
	closeNow := g.borrowers == 0
	g.borrowMu.Unlock()
	c.quicMu.Unlock()
	if closeNow {
		g.close("queqiao failed path probe drained")
	}
}

func (g *controlQUICGeneration) releaseBorrow() {
	g.borrowMu.Lock()
	g.borrowers--
	closeNow := g.draining && g.borrowers == 0
	g.borrowMu.Unlock()
	if closeNow {
		g.close("queqiao drained generation released")
	}
}
