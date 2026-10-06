package coded

import (
	"context"
	"fmt"
	"testing"

	"github.com/bojieli/queqiao/internal/protocol"
)

// Measure the frame serialization and queue handoff used by frameConn, without
// mixing network scheduling or FEC work into the allocation measurement.
func BenchmarkFrameQueue(b *testing.B) {
	for _, size := range []int{1500, 32 * 1024} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			p := &Path{pending: make(chan []byte, 1), done: make(chan struct{})}
			f := protocol.Frame{Header: protocol.Header{Version: protocol.Version, Type: protocol.TypeData}, Payload: make([]byte, size)}
			ctx := context.Background()
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf, err := protocol.AppendFrame(nil, f)
				if err != nil {
					b.Fatal(err)
				}
				if err := p.SendOwnedContext(ctx, buf); err != nil {
					b.Fatal(err)
				}
				<-p.pending
			}
		})
	}
}
