// Package egressproxy carries HTTP proxy bytes over a runtime-owned transport.
// It does not resolve destinations or hold model credentials.
package egressproxy

import (
	"context"
	"io"
	"net"
	"sync"
)

const Port = 1080
const maxConnections = 128

type Dial func(context.Context) (net.Conn, error)

// Serve closes accepted connections on cancellation and bounds concurrent copies.
// The caller supplies the listener and a fixed, trusted upstream dialer.
func Serve(ctx context.Context, listener net.Listener, dial Dial) error {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	slots := make(chan struct{}, maxConnections)
	for {
		downstream, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		select {
		case slots <- struct{}{}:
		default:
			_ = downstream.Close()
			continue
		}
		workers.Go(func() {
			defer func() { <-slots }()
			defer downstream.Close()
			cancelDownstream := context.AfterFunc(ctx, func() { _ = downstream.Close() })
			defer cancelDownstream()
			upstream, err := dial(ctx)
			if err != nil {
				return
			}
			defer upstream.Close()
			cancelUpstream := context.AfterFunc(ctx, func() { _ = upstream.Close() })
			defer cancelUpstream()
			var copies sync.WaitGroup
			copyHalf := func(dst, src net.Conn) {
				_, err := io.Copy(dst, src)
				if err != nil {
					_ = dst.Close()
					_ = src.Close()
					return
				}
				if half, ok := dst.(interface{ CloseWrite() error }); ok {
					_ = half.CloseWrite()
				} else {
					_ = dst.Close()
				}
			}
			copies.Go(func() { copyHalf(upstream, downstream) })
			copyHalf(downstream, upstream)
			copies.Wait()
		})
	}
}
