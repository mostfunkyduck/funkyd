package main

import (
	"net"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/mock"
)

type StubResponseWriter struct {
}

func (s *StubResponseWriter) WriteMsg(m *dns.Msg) error {
	return nil
}

type StubConnPool struct {
	mock.Mock
}

func (s *StubConnPool) Get() (ce ConnEntry, upstream Upstream) {
	server, c := net.Pipe()
	server.Close()
	ce = &connEntry{Conn: &dns.Conn{Conn: c}}
	upstream = Upstream{}
	return
}

// Adds a new connection to the pool
func (s *StubConnPool) Add(ce ConnEntry) (err error) {
	return nil
}

// Add a new upstream to the pool
func (s *StubConnPool) AddUpstream(r *Upstream) {

}

func (s *StubConnPool) CloseConnection(ce ConnEntry) {}

func (s *StubConnPool) Lock() {}

func (s *StubConnPool) Unlock() {}
func (s *StubConnPool) NewConnection(upstream Upstream, dialFunc DialFunc) (ce ConnEntry, err error) {
	server, c := net.Pipe()
	server.Close()
	ce = &connEntry{Conn: &dns.Conn{Conn: c}}
	err = nil
	return
}

// Returns the number of open connections in the pool
func (s *StubConnPool) Size() int {
	return 0
}

type StubJanitor struct {
	mock.Mock
}

// No need for the stub to do anything in this case
func (s *StubJanitor) Start(r *RecordCache) {
}

func (s *StubJanitor) Stop() {
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
// stub server = server with contents stubbed, not a stub of the server
// that's confusing, will fix eventually
func BuildStubServer() (Server, *StubDnsClient, error) {
	testClient := new(StubDnsClient)
	server, err := NewMutexServer(testClient, new(StubConnPool))
	server.(*MutexServer).Cache.StopCleaningCrew()
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
