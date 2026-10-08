package features

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUnresponsiveRedisHasBoundedShadowDeadline(t *testing.T) {
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
	reader, err := New("redis://" + listener.Addr().String() + "/0?read_timeout=5s&max_retries=5")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	start := time.Now()
	_, err = reader.Load(context.Background(), Scope{Subject: uuid.New(), Generation: 1, Enabled: true, AsOf: time.Now()}, []Reference{{PostID: uuid.New(), Revision: 1}})
	if err == nil || time.Since(start) > 200*time.Millisecond {
		t.Fatal("optional shadow read exceeded deadline", time.Since(start), err)
	}
	listener.Close()
	for conn := range accepted {
		conn.Close()
	}
}
