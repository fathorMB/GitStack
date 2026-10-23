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
