package main

import (
	"testing"
)

func TestQueryHandler(t *testing.T) {
	q := &queryHandler{
		outboundQueryChannel: make(chan Query, 1),
	}
	msg := &dns.Msg{}
	q.HandleDNS(new(StubResponseWriter), msg)
	qu := <-q.outboundQueryChannel
	if qu.Msg != msg {
		t.Fatalf("submitted message [%v] did not match message produced by HandleDNS [%v]", msg, qu.Msg)
	}
}
