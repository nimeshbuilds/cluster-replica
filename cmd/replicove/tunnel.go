package main

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"time"
)

// serveTunnel retains the local socket while forward reconnects to the owned
// runtime. It buffers no application payload and never replays a sent request.
func serveTunnel(ctx context.Context, listener net.Listener, backend *atomic.Int64) {
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go bridgeTunnel(ctx, conn, backend)
	}
}
func bridgeTunnel(ctx context.Context, local net.Conn, backend *atomic.Int64) {
	defer local.Close()
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var remote net.Conn
	for {
		if port := backend.Load(); port != 0 {
			conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(dialCtx, "tcp4", net.JoinHostPort("127.0.0.1", strconv.FormatInt(port, 10)))
			if err == nil {
				remote = conn
				break
			}
		}
		select {
		case <-dialCtx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	defer remote.Close()
	stop := context.AfterFunc(ctx, func() { _ = local.Close(); _ = remote.Close() })
	defer stop()
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		_, _ = io.Copy(remote, local)
		if tcp, ok := remote.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()
	_, _ = io.Copy(local, remote)
	_ = local.Close()
	_ = remote.Close()
	<-sent
}
