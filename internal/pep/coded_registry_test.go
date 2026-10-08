package pep

import (
	"io"
	"sync"
	"testing"

	"github.com/bojieli/queqiao/internal/coded"
)

type registryCarrier struct {
	done chan struct{}
	once sync.Once
}

func (c *registryCarrier) Send([]byte) error { return nil }
func (c *registryCarrier) Receive() ([]byte, error) {
	<-c.done
	return nil, io.EOF
}
func (c *registryCarrier) Close() error {
	c.once.Do(func() { close(c.done) })
	return nil
}

func registryPath(t *testing.T) *coded.Path {
	t.Helper()
	p := coded.New(&registryCarrier{done: make(chan struct{})}, coded.Config{Pending: 2})
	t.Cleanup(func() { _ = p.Close(); bulkDemuxs.Delete(p) })
	return p
}

func TestLateBulkReleaseCannotRecreateRetiredRegistryEntry(t *testing.T) {
	p := registryPath(t)
	d := connBulkDemux(p, 2)
	d.subscribe(1)
	// Connection cleanup has retired the registry, but the flow reader has
	// not yet run its deferred release. Cleanup must not allocate a new owner.
	bulkDemuxs.Delete(p)
	(&frameConn{bulk: p, bulkQueueFrames: 2}).releaseBulk(1)
	if _, present := bulkDemuxs.Load(p); present {
		t.Fatal("late reader release recreated the retired path and retains its transport")
	}
}

func TestClosedCodedPathCannotCreateDemultiplexer(t *testing.T) {
	p := registryPath(t)
	_ = p.Close()
	if d := connBulkDemux(p, 2); d != nil {
		t.Fatal("closed path acquired a new registry owner")
	}
}
