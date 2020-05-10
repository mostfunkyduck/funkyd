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
	raw := ret.Get(0)
	msg, ok := raw.(*dns.Msg)
	if !ok {
		panic(fmt.Sprintf("could not cast dns msg returnvalue [%v]", raw))
	}

	rawdur := ret.Get(1)
	dur, ok := rawdur.(time.Duration)
	if !ok {
		panic(fmt.Sprintf("couldn't convert [%v] to time.duration", rawdur))
	}
	return msg, dur, ret.Error(2)
}

func (m *MockDnsClient) Dial(address string) (conn *dns.Conn, err error) {
	ret := m.Called(address)
	raw := ret.Get(0)
	conn, ok := raw.(*dns.Conn)
	if !ok {
		panic(fmt.Sprintf("could not cast dns conn returnvalue [%v]", raw))
	}
	return conn, ret.Error(1)
}

func BenchmarkConnection(b *testing.B) {
	testClient := new(MockDnsClient)
	testConn := &dns.Conn{}
	testClient.On("Dial", mock.Anything).Return(testConn, nil)
	server, err := NewMutexServer(testClient)
	if err != nil {
		b.Fatalf("could no build server: [%s]", err)
	}

	if server.GetDnsClient() != testClient {
		b.Fatalf("got the wrong client when initializing the server")
	}

	c := make(chan string)
	for i := 0; i < 100; i++ {
		go func() {
			for s := range c {
				connEntry, err := server.MakeConnection(s)
				if err != nil {
					b.Fatalf("MakeConnection returned [%v][%s]", connEntry, err)
				}
				if connEntry.Conn != testConn {
					b.Fatalf("returned connection handle from make connection was not equal to test data: [%v] != [%v]", connEntry.Conn, testConn)
				}
			}
		}()
	}

	for i := 0; i < 100; i++ {
		c <- "1.2.3.4:123"
	}
	close(c)

	if !testClient.AssertExpectations(b) {
		b.Fatalf("assertion failed on connection")
	}

}

func buildTestResources(b *testing.B) (Server, *MockDnsClient, *dns.Conn) {
	testClient := new(MockDnsClient)
	testConn := &dns.Conn{}
	testClient.On("Dial", mock.Anything).Return(testConn, nil)
	server, err := NewMutexServer(testClient)
	if err != nil {
		b.Fatalf("could no build server: [%s]", err)
	}

	if server.GetDnsClient() != testClient {
		b.Fatalf("got the wrong client when initializing the server")
	}

	return server, testClient, testConn
}

type testFunc func(arg interface{}) interface{}

func testConcurrently(testF testFunc, arguments []interface{}) chan interface{} {
	r := make(chan interface{})
	argsChan := make(chan interface{})
	for i := 0; i < len(arguments); i++ {
		go func() {
			for arg := range argsChan {
				r <- testF(arg)
			}
		}()
	}
	for i := 0; i < len(arguments); i++ {
		argsChan <- arguments[i]
	}
	close(argsChan)
	return r
}

func BenchmarkCache(b *testing.B) {
	server, testClient, testConn := buildTestResources(b)
	results := testConcurrently(func(i interface{}) interface{} {
		connEntry, err := server.MakeConnection("1.2.3.4:123")
		if err != nil {
			b.Fatalf("MakeConnection returned [%v][%s]", connEntry, err)
		}
		if connEntry.Conn != testConn {
			b.Fatalf("returned connection handle from make connection was not equal to test data: [%v] != [%v]", connEntry.Conn, testConn)
		}
		return connEntry
	},
		make([]interface{}, 100))

	for a := 0; a < 100; a++ {
		<-results
	}
	if !testClient.AssertExpectations(b) {
		b.Fatalf("assertion failed on connection")
	}

}

func TestGetResolvers(t *testing.T) {
	server := &MutexServer{
		Resolvers: []*Resolver{
			&Resolver{
				Name: "a.b.c.d.e.f.g",
			},
			&Resolver{
				Name: "g.f.e.d.c.b.a",
			},
		},
	}

	names := server.GetResolvers()
	for _, r := range server.Resolvers {
		var found = false
		for _, n := range names {
			if n == r.Name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("could not find resolver name [%s] in list of resolvers [%v]", string(r.Name), server.Resolvers)
		}
	}
}
