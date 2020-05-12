package main

import (
	"github.com/miekg/dns"
	"github.com/stretchr/testify/mock"
	"net"
	"time"
)

type MockConn struct {
	mock.Mock
}

func (m *MockConn) Close() error {
	ret := m.Called()
	return ret.Error(0)
}

type StubConn struct {
	mock.Mock
}

func (s *StubConn) Close() error {
	return nil
}

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

// builds a stub server, connects to a stub client, returns the stuff
func BuildStubServer() (Server, *StubDnsClient, error) {
	testClient := new(StubDnsClient)
	server, err := NewMutexServer(testClient, InitConnPool())
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
