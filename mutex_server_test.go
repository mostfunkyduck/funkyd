package main

import (
	"github.com/miekg/dns"
	"github.com/stretchr/testify/mock"
	"net"
	"testing"
	"time"
)

type StubDnsClient struct {
	mock.Mock
}

func (m *StubDnsClient) ExchangeWithConn(s *dns.Msg, conn *dns.Conn) (r *dns.Msg, rtt time.Duration, err error) {
	return &dns.Msg{}, time.Duration(0), nil
}

func (m *StubDnsClient) Dial(address string) (conn *dns.Conn, err error) {
	server, client := net.Pipe()
	server.Close()
	return &dns.Conn{Conn: client}, nil
}

func buildTestResources() (Server, *StubDnsClient, error) {
	testClient := new(StubDnsClient)
	server, err := NewMutexServer(testClient)
	if err != nil {
		return server, testClient, err
	}

	server.AddResolver(
		&Resolver{
			Name: "a.b.c.d.e.f.g",
		},
	)
	server.AddResolver(
		&Resolver{
			Name: "g.f.e.d.c.b.a",
		},
	)

	return server, testClient, nil
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

// FIXME this needs to be done in the conn pool, with related stubs
/**
func BenchmarkConnectionParallel(b *testing.B) {
	server, testClient, err := buildTestResources()
	if err != nil {
		b.Fatalf("could not initialize server [%s]", err)
	}
	if server.GetDnsClient() != testClient {
		b.Fatalf("got the wrong client when initializing the server")
	}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			server.MakeConnection()
		}
	})

}
**/

// FIXME this needs to be done in the conn pool
/**
func BenchmarkConnectionSerial(b *testing.B) {
	server, _, err := buildTestResources()
	if err != nil {
		b.Fatalf("could not initialize server [%s]", err)
	}

	for i := 0; i < b.N; i++ {
		server.MakeConnection("1.2.3.4:53")
	}

}
**/

/** FIXME needs to be done in conn pool
func BenchmarkCacheParallel(b *testing.B) {
	server, _, err := buildTestResources()
	if err != nil {
		b.Fatalf("could not initialize server [%s]", err)
	}
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			server.MakeConnection("1.2.3.4:123")
		}
	})

}
**/

/** FIXME needs to be done in conn pool
func BenchmarkCacheSerial(b *testing.B) {
	server, _, err := buildTestResources()
	if err != nil {
		b.Fatalf("could not initialize server [%s]", err)
	}
	for i := 0; i < b.N; i++ {
		server.MakeConnection("1.2.3.4:123")
	}
}
**/
