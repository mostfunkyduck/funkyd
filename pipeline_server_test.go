package main

import (
	"fmt"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/mock"
	"testing"
	"time"
)

func TestQueryHandler(t *testing.T) {
	pw := NewPipelineServerWorker()
	pw.outboundQueryChannel = make(chan Query, 1)
	q := &PipelineQueryHandler{
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

func TestConnectorFailedAttempt(t *testing.T) {
	testClient := &MockClient{}
	testConnPool := &MockConnPool{}

	c := &connector{
		client:   testClient,
		connPool: testConnPool,
	}
	testConnPool.On("Get", mock.Anything).Return(nil, Upstream{Name: "example.com"}, nil)
	testConnPool.On("NewConnection", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("blah blah blah blah"))
	GetConfiguration().UpstreamRetries = 5
	qu := Query{}
	qu1, err := c.AssignConnection(qu)
	if err == nil {
		t.Fatalf("new connections are supposed to be failing, but no error came back from AssignConnection")
	}
	if (qu1 != Query{}) {
		t.Fatalf("Got populated query struct back from failed connection assignment: q: [%v]", qu1)
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
	pw := NewPipelineServerWorker()
	cache, err := NewCache()
	if err != nil {
		t.Fatalf("couldn't do cache: %s", err)
	}

	c := &PipelineCacher{
		pipelineServerWorker: pw,
		cache:                cache,
	}

	msg := buildRequest()
	qu := Query{}
	if cached, ok := c.CheckCache(qu); ok {
		t.Fatalf("no error returned when empty query was passed in, cached: [%v]", cached)
	}

	qu = Query{
		Msg: msg,
	}

	c.CacheQuery(qu)

	if cached, ok := c.CheckCache(qu); !ok {
		t.Fatalf("failed to find item that shold have been in cache, cached: [%v], qu: [%v], c [%v]", cached, qu, c)
	}
}

func TestCacherStart(t *testing.T) {
	msg := buildRequest()
	cw := NewPipelineServerWorker()
	cache := &MockCache{}

	cache.On("Get", "", 0).Return(Response{}, false).Once()
	cache.On("Get", "example.com", uint16(1)).Return(Response{Entry: *msg}, true)
	cache.On("Add", mock.Anything).Return()

	cacher := &PipelineCacher{
		pipelineServerWorker: cw,
		cachingChannel:       make(chan Query),
		cache:                cache,
	}
	cacher.Start()
	defer func() { cacher.cancelChannel <- true }()

	q := Query{}
	// does the cacher fail empty queries?
	cacher.inboundQueryChannel <- q
	<-cacher.failedQueryChannel

	q = Query{
		Msg: msg,
	}

	// does the cacher dispatch valid queries?
	cacher.cachingChannel <- q
	cacher.inboundQueryChannel <- q
	q = <-cacher.outboundQueryChannel
	if q.Reply == nil {
		t.Fatalf("did not get valid reply on cache hit, q: [%v]", q)
	}
}

func TestQuerier(t *testing.T) {
	pw := NewPipelineServerWorker()
	mockClient := &MockClient{}
	reply := &dns.Msg{}
	mockClient.On("ExchangeWithConn", mock.Anything, mock.Anything).Return(reply, time.Duration(1), nil)
	q := PipelineQuerier{
		pipelineServerWorker: pw,
		client:               mockClient,
	}
	testConnEntry := &ConnEntry{
		Conn: &dns.Conn{},
	}

	qu := Query{
		Conn: testConnEntry,
		Msg: &dns.Msg{
			Question: []dns.Question{
				dns.Question{
					Name:  "example.com",
					Qtype: 123,
				},
			},
		},
	}
	qu1, err := q.Query(qu)
	if err != nil {
		t.Fatalf("error during query: %s", err.Error())
	}

	if qu1.Reply.String() != reply.String() {
		t.Fatalf("got incorrect reply to dns query: [%v] != [%v]", qu1.Reply, reply)
	}

}

func TestQuerierStart(t *testing.T) {
	pw := NewPipelineServerWorker()
	mockClient := &MockClient{}
	a, err := dns.NewRR("example.com.	123	IN	A	10.0.0.0")
	if err != nil {
		t.Fatalf("couldn't create answer for query: %s", err)
	}
	reply := &dns.Msg{
		Answer: []dns.RR{
			a,
		},
	}
	mockClient.On("ExchangeWithConn", mock.Anything, mock.Anything).Return(reply, time.Duration(1), nil).Once()
	mockClient.On("ExchangeWithConn", mock.Anything, mock.Anything).Return(reply, time.Duration(1), fmt.Errorf("blah blah blah")).Once()

	q := PipelineQuerier{
		pipelineServerWorker: pw,
		client:               mockClient,
	}

	q.Start()
	defer func() { q.cancelChannel <- true }()
	testConnEntry := &ConnEntry{
		Conn: &dns.Conn{},
	}

	// First, test a good query
	qu := Query{
		Conn: testConnEntry,
		Msg: &dns.Msg{
			Question: []dns.Question{
				dns.Question{
					Name:  "example.com",
					Qtype: 123,
				},
			},
		},
	}

	q.inboundQueryChannel <- qu
	outcome := <-q.outboundQueryChannel
	if outcome.Reply.String() != reply.String() {
		t.Fatalf("got wrong reply from querier: outcome [%v] reply [%v]", outcome, reply)
	}

	q.inboundQueryChannel <- qu
	<-q.failedQueryChannel
}

func TestFinisherStart(t *testing.T) {
	pw := NewPipelineServerWorker()
	// make this unbuffered so that completion of the test
	// shows that the worker took from the q
	p := PipelineFinisher{
		pipelineServerWorker: pw,
	}
	pw.inboundQueryChannel = make(chan Query)
	p.servfailsChannel = make(chan Query)
	p.Start()
	defer func() { p.cancelChannel <- true }()
	writer := &MockResponseWriter{}
	qdt := &MockQueryDurationTimer{}
	writer.On("WriteMsg", mock.Anything).Return(nil)
	qdt.On("ObserveDuration").Return(time.Duration(100))
	q := Query{
		W:     writer,
		Msg:   &dns.Msg{},
		Reply: &dns.Msg{},
		Timer: qdt,
	}

	p.inboundQueryChannel <- q

	p.servfailsChannel <- q
}

func TestFinisherStartErrors(t *testing.T) {
	pw := NewPipelineServerWorker()
	p := PipelineFinisher{
		pipelineServerWorker: pw,
	}
	pw.inboundQueryChannel = make(chan Query)
	p.servfailsChannel = make(chan Query)
	p.Start()
	defer func() { p.cancelChannel <- true }()

	q := buildQuery()
	q.W.(*MockResponseWriter).On("WriteMsg", mock.Anything).Return(fmt.Errorf("argh"))
	q.Timer.(*MockQueryDurationTimer).On("ObserveDuration").Return(time.Duration(100))

	p.inboundQueryChannel <- q

	p.servfailsChannel <- q
}

func TestEndToEnd(t *testing.T) {
	client := &MockClient{}
	cpool := &MockConnPool{}
	writer := &MockResponseWriter{}

	entry := &ConnEntry{}
	upstream := Upstream{
		Name: "example.com",
	}
	reply := buildAnswer()

	writer.On("WriteMsg", reply).Return(nil)
	client.On("ExchangeWithConn", mock.Anything, mock.Anything).Return(reply, time.Duration(100), nil)
	cpool.On("Get").Return(nil, upstream, nil)
	cpool.On("NewConnection", mock.Anything, mock.Anything).Return(entry, nil)

	request := buildRequest()
	qh, cancels, err := NewPipelineServer(client, cpool)
	if err != nil {
		t.Fatalf("could not build pipeline server: %s", err)
	}
	defer func() {
		for _, each := range cancels {
			each <- true
		}
	}()

	qh.HandleDNS(writer, request)

	/**
	WaitForCondition(10, func() bool {
		return len(writer.Calls) == len(writer.ExpectedCalls)
	})

	writer.AssertExpectations(t)
	client.AssertExpectations(t)
	cpool.AssertExpectations(t)
	**/
}
