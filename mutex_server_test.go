package main

import (
	"fmt"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/mock"
	"testing"
	"time"
)

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

func buildTestResources() (Server, *StubDnsClient, error) {
	return BuildStubServer()
}

func buildTestServer(testClient *MockDnsClient, testPool *MockConnPool) (Server, error) {
	server, err := NewMutexServer(testClient, testPool)
	if err != nil {
		return server, err
	}

	return server, nil
}

func TestRecursiveQueryErrors(t *testing.T) {
	cl := new(MockDnsClient)
	pool := new(MockConnPool)
	server, err := buildTestServer(cl, pool)
	if err != nil {
		t.Fatalf("could not build test resources: [%v]: %s", server, err)
	}
	cl.On("ExchangeWithConn", mock.Anything, mock.Anything).Return(&dns.Msg{}, time.Duration(0), fmt.Errorf("no DNS for you!"))
	pool.On("Get").Return(&ConnEntry{Conn: &dns.Conn{}}, Upstream{}, nil)
	pool.On("CloseConnection", mock.Anything).Return(nil)
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
			server.HandleDNS(&StubResponseWriter{}, testMsg)
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
		server.HandleDNS(&StubResponseWriter{}, testMsg)
	}
}
