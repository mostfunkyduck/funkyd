package main

import (
	"fmt"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/mock"
	"net"
	"testing"
	"time"
)

type TestResponseWriter struct {
}

func (t *TestResponseWriter) WriteMsg(m *dns.Msg) error {
	return nil
}

type MockDnsClient struct {
	mock.Mock
}

func (m *MockDnsClient) ExchangeWithConn(s *dns.Msg, conn *dns.Conn) (r *dns.Msg, rtt time.Duration, err error) {
	ret := m.Called(s, conn)
	return ret.Get(0).(*dns.Msg), ret.Get(1).(time.Duration), ret.Error(2)
}

func (m *MockDnsClient) Dial(address string) (conn *dns.Conn, err error) {
	ret := m.Called(address)
	return ret.Get(0).(*dns.Conn), ret.Error(1)
}

type MockConnPool struct {
	mock.Mock
}

func buildTestResources() (Server, *StubDnsClient, error) {
	return BuildStubServer()
}

func buildTestMocks(mockDial bool) (Server, *MockDnsClient, error) {
	testClient := new(MockDnsClient)
	s, client := net.Pipe()
	s.Close()
	if mockDial {
		testClient.On("Dial", mock.Anything).Return(&dns.Conn{Conn: client}, nil)
	}
	server, err := NewMutexServer(testClient, NewConnPool())
	if err != nil {
		return server, testClient, err
	}

	server.AddUpstream(
		&Upstream{
			Name: "a.b.c.d.e.f.g",
		},
	)
	server.AddUpstream(
		&Upstream{
			Name: "g.f.e.d.c.b.a",
		},
	)

	return server, testClient, nil
}

func TestRecursiveQueryErrors(t *testing.T) {
	server, cl, err := buildTestMocks(true)
	if err != nil {
		t.Fatalf("could not build test resources: [%v]: %s", server, err)
	}
	cl.On("ExchangeWithConn", mock.Anything, mock.Anything).Return(&dns.Msg{}, time.Duration(0), fmt.Errorf("no DNS for you!"))
	if r, source, err := server.RecursiveQuery("example.com", dns.TypeA); err == nil {
		t.Fatalf("exchange errors didn't bubble up to the caller r[%v] source[%v]", r, source)
	}
	cl.AssertExpectations(t)
}

/** BENCHMARKS **/
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
