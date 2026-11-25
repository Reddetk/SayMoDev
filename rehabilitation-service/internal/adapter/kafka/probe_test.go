package kafka

import (
	"context"
	"net"
	"testing"
	"time"
)

// Брокер принимает TCP и молчит: Ping обязан вернуться по deadline, а не висеть.
func TestPingHonorsDeadlineOnSilentBroker(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- conn
	}()

	probe := New([]string{ln.Addr().String()}, 100*time.Millisecond)

	start := time.Now()
	err = probe.Ping(context.Background())
	elapsed := time.Since(start)

	select {
	case conn := <-accepted:
		_ = conn.Close()
	default:
	}

	if err == nil {
		t.Fatal("want timeout error from silent broker, got nil")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Ping blocked for %s, want bounded by probe deadline", elapsed)
	}
}
