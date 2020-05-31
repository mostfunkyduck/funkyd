package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/mock"
)

type MockDNSClient struct {
	mock.Mock
}

func (m *MockDNSClient) ExchangeWithConn(s *dns.Msg, conn *dns.Conn) (r *dns.Msg, rtt time.Duration, err error) {
	ret := m.Called(s, conn)
	return ret.Get(0).(*dns.Msg), ret.Get(1).(time.Duration), ret.Error(2)
}

func (m *MockDNSClient) Dial(address string) (conn *dns.Conn, err error) {
	ret := m.Called(address)
	return ret.Get(0).(*dns.Conn), ret.Error(1)
}

func buildTestResources() (Server, error) {
	return BuildStubServer()
}

func buildTestServer(testClient Client, testPool ConnPool) Server {
	server := NewMutexServer(testClient, testPool)

	return server
}

func TestRecursiveQueryErrors(t *testing.T) {
	cl := new(MockDNSClient)
	pool := new(MockConnPool)
	server := buildTestServer(cl, pool)

	// nolint:lll
	cl.On("ExchangeWithConn", mock.Anything, mock.Anything).Return(&dns.Msg{}, time.Duration(0), fmt.Errorf("no DNS for you!"))
	pool.On("Get").Return(&connEntry{Conn: &dns.Conn{}}, Upstream{}, nil)
	pool.On("CloseConnection", mock.Anything).Return(nil)
	if r, source, err := server.RecursiveQuery("example.com", dns.TypeA); err == nil {
		t.Fatalf("exchange errors didn't bubble up to the caller r[%v] source[%v]", r, source)
	}
	cl.AssertExpectations(t)
}

// BENCHMARKS.
func BenchmarkServeDNSParallel(b *testing.B) {
	server, err := buildTestResources()
	if err != nil {
		b.Fatalf("could not initialize server [%s]", err)
	}
	testMsg := new(dns.Msg)
	testMsg.SetQuestion("example.com", dns.TypeA)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			server.HandleDNS(&StubResponseWriter{}, testMsg)
		}
	})
}

func BenchmarkServeDNSSerial(b *testing.B) {
	server, err := buildTestResources()
	if err != nil {
		b.Fatalf("could not initialize server [%s]", err)
	}
	testMsg := new(dns.Msg)
	testMsg.SetQuestion("example.com", dns.TypeA)
	for i := 0; i < b.N; i++ {
		server.HandleDNS(&StubResponseWriter{}, testMsg)
	}
}
