package main

import (
	"fmt"
	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
	"time"
)

type PipelineServerWorker interface {
	// starts the worker gr(s)
	Start()

	// Dispatches a query to the next step in the pipeline
	Dispatch(q Query)

	// Dispatches a query to be failed
	Fail(q Query)
}

type pipelineServerWorker struct {
	PipelineServerWorker
	// channel for accepting new queries
	inboundQueryChannel chan Query

	// channel for dispatching failed queries
	failedQueryChannel chan Query

	// channel for dispatching successful queries
	outboundQueryChannel chan Query
}

// checks queries against the cache
type Cacher interface {
	PipelineServerWorker

	// determines if this query is cached
	CheckCache(q Query) (Response, bool)
}

// does actual queries
type Querier interface {
	PipelineServerWorker

	// looks up records
	Query(q Query) (Query, error)
}

// Pairs outbound queries with connections
type Connector interface {
	PipelineServerWorker

	// Assigns a given connection to a query
	AssignConnection(q Query) Query

	// adds an upstream to an internal list
	AddUpstream(u *Upstream)
}

// Handles initial connection acceptance
type QueryHandler interface {
	PipelineServerWorker
	ServeDNS(w dns.ResponseWriter, r *dns.Msg)
}
type Query struct {
	// The query payload
	Msg *dns.Msg

	// The target upstream
	Upstream Upstream

	// The connection to use
	Conn *ConnEntry

	// the prometheus timer to use for this query
	Timer *prometheus.Timer

	// the reply received for this query
	Reply *dns.Msg
}

type connector struct {
	pipelineServerWorker

	// connection pool
	connPool ConnPool

	// Client for making outbound connections
	client Client
}

type cacher struct {
	pipelineServerWorker

	// the actual cache
	cache *RecordCache
}

// handles initial inbound query acceptance
type queryHandler struct {
	pipelineServerWorker
}

type querier struct {
	pipelineServerWorker

	// client to send outbound queries with
	client Client
}

func (p *pipelineServerWorker) Dispatch(q Query) {
	p.outboundQueryChannel <- q
}

func (p *pipelineServerWorker) Fail(q Query) {
	p.failedQueryChannel <- q
}
func (c *connector) AddUpstream(u *Upstream) {
	c.connPool.AddUpstream(u)
}

func (c connector) Start() {
	go func() {
		for query := range c.inboundQueryChannel {
			assignedQuery, err := c.AssignConnection(query)
			if err != nil {
				Logger.Log(LogMessage{
					Level: ERROR,
					Context: LogContext{
						"what":  "connection manager failed to assign connection to query",
						"query": assignedQuery.Msg.String(),
						"next":  "dispatching to be SERVFAILed",
					},
				})
				c.Fail(assignedQuery)
			}
			c.Dispatch(assignedQuery)
		}
	}()
}

func (c *connector) AssignConnection(q Query) (assignedQuery Query, err error) {
	assignedQuery = q
	connEntry, upstream := c.connPool.Get()
	if (upstream != Upstream{}) {
		// we need to make a new connection
		connEntry, err = c.connPool.NewConnection(upstream, c.client.Dial)
		if err != nil {
			Logger.Log(LogMessage{
				Level: WARNING,
				Context: LogContext{
					"what":     "failed to make connection to upstream",
					"address":  upstream.GetAddress(),
					"upstream": Logger.Sprintf(DEBUG, "%v", upstream),
				},
			})
			return Query{}, err
		}
	}
	assignedQuery.Conn = connEntry
	return assignedQuery, nil
}
func (q *queryHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	q.HandleDNS(w, r)
}

func (s *queryHandler) HandleDNS(w ResponseWriter, r *dns.Msg) {
	TotalDnsQueriesCounter.Inc()
	queryTimer := prometheus.NewTimer(QueryTimer)

	QueuedQueriesGauge.Inc()
	s.Dispatch(Query{
		Msg:   r,
		Timer: queryTimer,
	})
	QueuedQueriesGauge.Dec()
}

func (q *querier) Query(qu Query) (query Query, err error) {
	RecursiveQueryCounter.Inc()

	m := &dns.Msg{}
	m.SetQuestion(qu.Msg.Question[0].Name, qu.Msg.Question[0].Qtype)
	m.RecursionDesired = true

	config := GetConfiguration()

	for i := 0; i <= config.UpstreamRetries; i++ {
		if reply, err := attemptExchange(m, qu.Conn, q.client); err == nil {
			qu.Reply = reply
			break
		}
		if err != nil {
			Logger.Log(NewLogMessage(
				WARNING,
				LogContext{
					"what":  "failed exchange with upstreams",
					"error": err.Error(),
					"next":  fmt.Sprintf("retrying until config.UpstreamRetries is met. currently on attempt [%d]/[%d]", i, config.UpstreamRetries),
				},
				nil,
			))
		}
		// continue trying
	}

	if err != nil {
		// we failed to complete any exchanges
		Logger.Log(NewLogMessage(
			WARNING,
			LogContext{
				"what":    "failed to complete any exchanges with upstreams",
				"error":   err.Error(),
				"note":    "this is the most recent error, other errors may have been logged during the failed attempt(s)",
				"address": qu.Conn.GetAddress(),
				"next":    "aborting query attempt",
			},
			nil,
		))
		return qu, fmt.Errorf("failed to complete any exchanges with upstreams: %s", err.Error())
	}
	return qu, nil
}

func (q *querier) Start() {

	go func() {
		for query := range q.inboundQueryChannel {
			query, err := q.Query(query)
			if err != nil {
				Logger.Log(LogMessage{
					Level: ERROR,
					Context: LogContext{
						"what":  "error retrieving record for domain",
						"query": query.Msg.String(),
						"error": err.Error(),
						"next":  "failing query",
					},
				})
				q.Fail(query)
				continue
			}
			q.Dispatch(query)
		}
	}()
}

func (c *cacher) CheckCache(q Query) (result Response, ok bool) {
	if (q.Msg == nil) || len(q.Msg.Question) < 1 {
		return Response{}, false
	}
	return c.cache.Get(q.Msg.Question[0].Name, q.Msg.Question[0].Qtype)
}

func (c *cacher) CacheQuery(q Query) {
	question := q.Msg.Question[0]
	r := Response{
		Entry:        *q.Msg,
		CreationTime: time.Now(),
		Name:         question.Name,
		Qtype:        question.Qtype,
	}
	c.cache.Add(r)
}

func newPipelineServerWorker() pipelineServerWorker {
	return pipelineServerWorker{
		inboundQueryChannel:  make(chan Query),
		outboundQueryChannel: make(chan Query),
		failedQueryChannel:   make(chan Query),
	}
}

func NewQueryHandler(cl Client, pool ConnPool) (err error) {
	config := GetConfiguration()
	client := cl
	if client == nil {
		var err error
		client, err = BuildClient()
		if err != nil {
			return fmt.Errorf("could not build client [%s]", err.Error())
		}
	}

	ret := &queryHandler{
		pipelineServerWorker: newPipelineServerWorker(),
	}

	// query handlers pas to cachers which pass to connectors which pass to
	// queriers which pass to finishers

	// INIT cacher
	cacheWorker := newPipelineServerWorker()
	cacheWorker.inboundQueryChannel = ret.outboundQueryChannel
	cache, err := NewCache()
	if err != nil {
		return fmt.Errorf("could not create record cache for cacher: %s", err.Error())
	}

	cachr := &cacher{
		pipelineServerWorker: cacheWorker,
		cache:                cache,
	}
	cachr.Start()

	// INIT connector
	cmWorker := newPipelineServerWorker()
	cmWorker.inboundQueryChannel = cacheWorker.outboundQueryChannel
	cm := &connector{
		pipelineServerWorker: cmWorker,
		client:               client,
		connPool:             NewConnPool(),
	}
	for _, name := range config.Upstreams {
		upstream := &Upstream{
			Name: name,
		}
		cm.AddUpstream(upstream)
	}

	cm.Start()

	// INIT querier
	querierWorker := newPipelineServerWorker()
	querierWorker.inboundQueryChannel = cm.outboundQueryChannel
	querier := &querier{
		client: client,
	}
	querier.Start()
	return nil
}
