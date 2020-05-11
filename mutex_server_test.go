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

	server.SetResolvers([]*Resolver{
		&Resolver{
			Name: "a.b.c.d.e.f.g",
		},
		&Resolver{
			Name: "g.f.e.d.c.b.a",
		},
	})

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
			server.MakeConnection("1.2.3.4:53")
		}
	})

}

func BenchmarkConnectionSerial(b *testing.B) {
	server, _, err := buildTestResources()
	if err != nil {
		b.Fatalf("could not initialize server [%s]", err)
	}

	for i := 0; i < b.N; i++ {
		server.MakeConnection("1.2.3.4:53")
	}

}

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

func BenchmarkCacheSerial(b *testing.B) {
	server, _, err := buildTestResources()
	if err != nil {
		b.Fatalf("could not initialize server [%s]", err)
	}
	for i := 0; i < b.N; i++ {
		server.MakeConnection("1.2.3.4:123")
	}
}

func TestGetResolvers(t *testing.T) {
	server, _, err := buildTestResources()
	if err != nil {
		t.Fatalf("could not initialize server [%s]", err)
	}
	resolvers := server.GetResolvers()
	for _, r := range server.(*MutexServer).Resolvers {
		var found = false
		for _, r1 := range resolvers {
			if r1.Name == r.Name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("could not find resolver name [%s] in list of resolvers [%v]", string(r.Name), server.(*MutexServer).Resolvers)
		}
	}
}
