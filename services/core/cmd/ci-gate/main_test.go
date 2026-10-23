package main

import (
	"net"
	"testing"
	"time"
)

func TestWaitBlocked(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if waitBlocked(l.Addr().String(), 50*time.Millisecond) {
		t.Fatal("listener raggiungibile")
	}
	_ = l.Close()
	if !waitBlocked(l.Addr().String(), time.Second) {
		t.Fatal("listener chiuso non rilevato")
	}
}

func TestWaitBlockedAll(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "127.0.0.1:1"
	if waitBlockedAll([]string{closed, l.Addr().String()}, 50*time.Millisecond) {
		t.Fatal("un endpoint interno raggiungibile non deve superare il gate")
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if !waitBlockedAll([]string{closed, l.Addr().String()}, time.Second) {
		t.Fatal("tutti gli endpoint bloccati devono superare il gate")
	}
}
