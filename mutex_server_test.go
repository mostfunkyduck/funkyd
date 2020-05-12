package main

import (
	"github.com/miekg/dns"
	"testing"
)

func buildTestResources() (Server, *StubDnsClient, error) {
	return BuildStubServer()
}

type TestResponseWriter struct {
}

func (t *TestResponseWriter) WriteMsg(m *dns.Msg) error {
	return nil
}
func BenchmarkServeDNSParallel(b *testing.B) {
	server, _, err := buildTestResources()
	if err != nil {
		b.Fatalf("could not initialize server [%s]", err)
	}
	testMsg := new(dns.Msg)
	testMsg.SetQuestion("example.com", dns.TypeA)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			server.HandleDNS(&TestResponseWriter{}, testMsg)
		}
	})
}

func BenchmarkServeDNSSerial(b *testing.B) {
	server, _, err := buildTestResources()
	if err != nil {
		b.Fatalf("could not initialize server [%s]", err)
	}
	testMsg := new(dns.Msg)
	testMsg.SetQuestion("example.com", dns.TypeA)
	for i := 0; i < b.N; i++ {
		server.HandleDNS(&TestResponseWriter{}, testMsg)
	}
}
