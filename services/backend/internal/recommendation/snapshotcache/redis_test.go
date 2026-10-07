package snapshotcache

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUnresponsiveRedisCannotConsumeFeedDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
		close(accepted)
	}()
	cache, err := New("redis://" + listener.Addr().String() + "/0?read_timeout=5s&max_retries=5")
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	start := time.Now()
	_, err = cache.Load(context.Background(), Scope{Token: uuid.New(), Query: "viewer-bound-query", Generation: 1})
	if err == nil || time.Since(start) > 200*time.Millisecond {
		t.Fatal("unresponsive optional cache exceeded budget", time.Since(start), err)
	}
	listener.Close()
	for conn := range accepted {
		conn.Close()
	}
}
