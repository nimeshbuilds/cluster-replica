package main

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestTunnelRetainsEndpointDuringReconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	front, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer front.Close()
	var target atomic.Int64
	stopped := make(chan struct{})
	go func() { serveTunnel(ctx, front, &target); close(stopped) }()
	// Accept before a replacement transport exists: no refused connection and no
	// application replay. The bytes traverse the new upstream only once.
	client, err := net.Dial("tcp4", front.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := client.Write([]byte("exactly-once")); err != nil {
		t.Fatal(err)
	}
	server, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	received := make(chan string, 1)
	go func() {
		conn, err := server.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		data := make([]byte, 12)
		_, err = io.ReadFull(conn, data)
		if err == nil {
			received <- string(data)
			_, _ = conn.Write(data)
		}
	}()
	target.Store(int64(server.Addr().(*net.TCPAddr).Port))
	got := make([]byte, 12)
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "exactly-once" || <-received != "exactly-once" {
		t.Fatal("payload changed or repeated")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("listener leaked after cancel")
	}
	if conn, err := net.DialTimeout("tcp4", front.Addr().String(), time.Second); err == nil {
		conn.Close()
		t.Fatal("listener survived cancelled session")
	}
}
