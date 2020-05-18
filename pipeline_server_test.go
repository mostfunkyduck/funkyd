package main

import (
	"github.com/miekg/dns"
	"time"
	"github.com/stretchr/testify/mock"
	"testing"
)

func TestQueryHandler(t *testing.T) {
	pw := newPipelineServerWorker()
	pw.outboundQueryChannel = make(chan Query, 1)
	q := &queryHandler{
		pipelineServerWorker: pw,
	}
	msg := &dns.Msg{}
	m := &MockResponseWriter{}
	m.On("WriteMsg", mock.Anything).Return(nil)
	q.HandleDNS(m, msg)
	qu := <-q.outboundQueryChannel
	if qu.Msg != msg {
		t.Fatalf("submitted message [%v] did not match message produced by HandleDNS [%v]", msg, qu.Msg)
	}
}

func TestConnectorReuseConn(t *testing.T) {
	testClient := &MockClient{}
	testConnPool := &MockConnPool{}
	testConnEntry := &ConnEntry{
		Conn: &dns.Conn{},
	}
	c := &connector{
		client:   testClient,
		connPool: testConnPool,
	}
	testConnPool.On("Get", mock.Anything).Return(testConnEntry, Upstream{})
	qu := Query{}
	qu1, err := c.AssignConnection(qu)
	if err != nil {
		t.Fatalf("could not assign connection: [%v] [%v]", qu, c)
	}

	if qu1.Conn == nil {
		t.Fatalf("successful return from connector.AssignConnection didn't attach a conn to the query struct: [%v]", qu1)
	}
}

func TestConnectorNewConn(t *testing.T) {
	testConnPool := &MockConnPool{}
	testConnEntry := &ConnEntry{
		Conn: &dns.Conn{},
	}
	c := &connector{
		client:   &MockClient{},
		connPool: testConnPool,
	}
	testConnPool.On("Get", mock.Anything).Return(&ConnEntry{}, Upstream{Name: "example.com"})
	testConnPool.On("NewConnection", mock.Anything, mock.Anything).Return(testConnEntry, nil)
	qu := Query{}
	qu1, err := c.AssignConnection(qu)
	if err != nil {
		t.Fatalf("could not assign connection: [%v] [%v]", qu, c)
	}

	if qu1.Conn != testConnEntry {
		t.Fatalf("stored connection was not used by connector: [%v] [%v]", qu1.Conn, testConnEntry)
	}
}

func TestCacher(t *testing.T) {
	pw := newPipelineServerWorker()
	cache, err := NewCache()
	if err != nil {
		t.Fatalf("couldn't init cache: %s", err.Error())
	}

	c := &cacher{
		pipelineServerWorker: pw,
		cache:                cache,
	}
	qu := Query{}
	cached, ok := c.CheckCache(qu)
	if ok {
		t.Fatalf("no error returned when empty query was passed in, cached: [%v]", cached)
	}

	msg := &dns.Msg{
		Question: []dns.Question{
			dns.Question{
				Name:   "example.com",
				Qtype:  1,
				Qclass: 1,
			},
		},
	}
	qu = Query{
		Msg: msg,
	}

	c.CacheQuery(qu)

	cached, ok = c.CheckCache(qu)
	if !ok {
		t.Fatalf("failed to find item that shold have been in cache, cached: [%v], qu: [%v], c [%v]", cached, qu, c)
	}
}

func TestQuerier(t *testing.T) {
	pw := newPipelineServerWorker()
	mockClient := &MockClient{}
	reply := &dns.Msg{}
	mockClient.On("ExchangeWithConn", mock.Anything, mock.Anything).Return(reply, time.Duration(1), nil)
	q := querier{
		pipelineServerWorker: pw,
		client: mockClient,
	}
	testConnEntry := &ConnEntry {
		Conn: &dns.Conn{},
	}

	qu := Query {
		Conn:	testConnEntry,
		Msg: &dns.Msg{
			Question: []dns.Question{
				dns.Question {
					Name: "example.com",
					Qtype: 123,
				},
			},
		},
	}
	qu1, err := q.Query(qu)
	if err != nil {
		t.Fatalf("error during query: %s", err.Error())
	}

	if qu1.Reply != reply {
		t.Fatalf("got incorrect reply to dns query: [%v] != [%v]", qu1.Reply, reply)
	}

}
