package main

import (
	"fmt"
	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
	"time"
)

// Basic functions for a pipeline worker
type PipelineServerWorker interface {
	// starts the worker gr(s)
	Start()

	// Dispatches a query to the next step in the pipeline
	Dispatch(q Query)

	// Dispatches a query to be failed
	Fail(q Query)
}

// interface for the timer in the query struct, mainly to avoid
// tightly coupling to prometheus
type QueryDurationTimer interface {
	ObserveDuration() time.Duration
}

// Basic implementation for a pipeline worker
type pipelineServerWorker struct {
	PipelineServerWorker
	// channel for accepting new queries
	inboundQueryChannel chan Query

	// channel for dispatching failed queries
	failedQueryChannel chan Query

	// channel for dispatching successful queries
	outboundQueryChannel chan Query

	// cancel channel - mainly for testing, nobody
	// else is likely to have this channel
	// TODO evaluate some kind of global cancellation
	cancelChannel chan bool
}

/** Worker Implementations **/

// Pairs outbound queries with connections
// Dispatch: connection is successful, forward to PipelineQuerier
// Fail: connection failure, forward to PipelineFinisher for servfail
type connector struct {
	pipelineServerWorker

	// connection pool
	connPool ConnPool

	// Client for making outbound connections
	client Client
}

// checks queries against the cache
// Dispatch: cache hit - forwards to PipelineQuerier
// Fail: cache miss - forward to connector

type PipelineCacher struct {
	pipelineServerWorker

	// the actual cache
	cache Cache

	//a channel for inbound queries to be cached
	cachingChannel chan Query
}

// Handles initial connection acceptance
// Dispatch: forwards to PipelineCacher
// Fail: forwards to PipelineFinisher for servfailing

type PipelineQueryHandler struct {
	pipelineServerWorker
}

// does actual queries
// Dispatch: query successful, forward to replier for happy response
// Fail: send back to connector for a new connection, this will keep happening until the connector gives up
type PipelineQuerier struct {
	pipelineServerWorker

	// client to send outbound queries with
	client Client
}

type PipelineFinisher struct {
	pipelineServerWorker

	// dedicated channel for servfails, this makes
	// the code more readable than just overriding one of the other channels
	servfailsChannel chan Query
}

// Context variable for queries, passed through pipeline to all workers
type Query struct {
	// The query payload
	Msg *dns.Msg

	// The target upstream
	Upstream Upstream

	// The connection to use
	Conn *ConnEntry

	// how many times this query has had to retry a connection
	ConnectionRetries int

	// the prometheus timer to use for this query
	Timer QueryDurationTimer

	// the reply received for this query
	Reply *dns.Msg

	// the response writer to reply on
	W ResponseWriter
}

/** base worker functions **/
func (p *pipelineServerWorker) Dispatch(q Query) {
	p.outboundQueryChannel <- q
}

func (p *pipelineServerWorker) Fail(q Query) {
	p.failedQueryChannel <- q
}

/** connector **/
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
				// fail to PipelineFinisher
				c.Fail(assignedQuery)
			}
			// FIXME there seems to be a case where the PipelineQuerier will be blocked
			// FIXME on the connector accepting whilst the connector is blocked
			// FIXME on the PipelineQuerier rejecting messages
			// FIXME one possible solution might be to have the PipelineQuerier do connection
			// FIXME mgm't, essentially collapsing this into that, there'd be more work
			// FIXME than i want to handle in the PipelineQuerier, but that's better than deadlock
			// dispatch to PipelineQuerier
			c.Dispatch(assignedQuery)
		}
	}()
}

func (c *connector) AssignConnection(q Query) (assignedQuery Query, err error) {
	assignedQuery = q
	connEntry, upstream := c.connPool.Get()
	if (upstream != Upstream{}) {
		var finalError error
		// we need to make a new connection
		for i := assignedQuery.ConnectionRetries; i < GetConfiguration().UpstreamRetries; i++ {
			connEntry, err = c.connPool.NewConnection(upstream, c.client.Dial)
			assignedQuery.ConnectionRetries++
			if err != nil {
				Logger.Log(LogMessage{
					Level: WARNING,
					Context: LogContext{
						"what":     "failed to make connection to upstream",
						"attempt":  fmt.Sprintf("%d/%d", i, GetConfiguration().UpstreamRetries),
						"address":  upstream.GetAddress(),
						"upstream": Logger.Sprintf(DEBUG, "%v", upstream),
					},
				})
				finalError = fmt.Errorf("failed to connect to [%s]: %s: %s", upstream.GetAddress(), err, finalError)

				continue
			}

			break
		}
		if err != nil {
			return Query{}, fmt.Errorf("failed to make any connections to upstream %s: [%s]", upstream.GetAddress(), finalError)
		}
	}
	assignedQuery.Conn = connEntry
	return assignedQuery, nil
}

/** query handler **/
func (q *PipelineQueryHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	q.HandleDNS(w, r)
}

func (s *PipelineQueryHandler) HandleDNS(w ResponseWriter, r *dns.Msg) {
	TotalDnsQueriesCounter.Inc()
	queryTimer := prometheus.NewTimer(QueryTimer)

	QueuedQueriesGauge.Inc()
	// dispatch to PipelineCacher
	s.Dispatch(Query{
		Msg:   r,
		Timer: queryTimer,
	})
	QueuedQueriesGauge.Dec()
}

/** PipelineQuerier **/
func (q *PipelineQuerier) Query(qu Query) (query Query, err error) {
	query = qu
	RecursiveQueryCounter.Inc()

	m := &dns.Msg{}
	m.SetQuestion(query.Msg.Question[0].Name, query.Msg.Question[0].Qtype)
	m.RecursionDesired = true

	var reply *dns.Msg
	if reply, err = attemptExchange(m, query.Conn, q.client); err != nil {
		Logger.Log(NewLogMessage(
			WARNING,
			LogContext{
				"what":  "failed exchange with upstreams",
				"error": err.Error(),
			},
			nil,
		))
		// this connection is tainted, try another one
		query.ConnectionRetries++
		// bail
		return query, fmt.Errorf("failed to exchange query %s: %s", m.String(), err)
	}

	query.Reply = reply.Copy()
	return query, nil
}

func (q *PipelineQuerier) Start() {

	go func() {
		for {
			select {
			case _ = <-q.cancelChannel:
				return
			case query := <-q.inboundQueryChannel:
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
					// fail to PipelineFinisher
					q.Fail(query)
					continue
				}
				// dispatch to replier
				q.Dispatch(query)
			}
		}
	}()
}

/** PipelineCacher **/

// checks for a query in the cache, the cache object handles
// expiry
func (c *PipelineCacher) CheckCache(q Query) (result Response, ok bool) {
	if (q.Msg == nil) || len(q.Msg.Question) < 1 {
		return Response{}, false
	}
	return c.cache.Get(q.Msg.Question[0].Name, q.Msg.Question[0].Qtype)
}

// adds a connection to the cache
func (c *PipelineCacher) CacheQuery(q Query) {
	question := q.Msg.Question[0]
	r := Response{
		Entry:        *q.Msg,
		CreationTime: time.Now(),
		Name:         question.Name,
		Qtype:        question.Qtype,
	}
	c.cache.Add(r)
}

func (c *PipelineCacher) Start() {
	go func() {
		for {
			select {
			case q := <-c.inboundQueryChannel:
				if resp, ok := c.CheckCache(q); ok {
					q.Reply = resp.Entry.Copy()
					// pass to PipelineQuerier
					c.Dispatch(q)
					break
				}
				//	pass to connector
				c.Fail(q)
			case q := <-c.cachingChannel:
				c.CacheQuery(q)
				// no need to do anything else
			case _ = <-c.cancelChannel:
				logCancellation("PipelineCacher")
				return
			}
		}
	}()
}

func (p *PipelineFinisher) Start() {
	go func() {
		for {
			select {
			case _ = <-p.cancelChannel:
				return
			case q := <-p.servfailsChannel:
				failingQuery := q
				failingQuery.Reply = &dns.Msg{}
				failingQuery.Reply.SetRcode(failingQuery.Msg, dns.RcodeServerFailure)
				if err := q.W.WriteMsg(q.Reply); err != nil {
					Logger.Log(LogMessage{
						Level: CRITICAL,
						Context: LogContext{
							"what":  "failed to write servfail reply to client",
							"error": err.Error(),
							"reply": q.Reply.String(),
						},
					})
				}
				duration := q.Timer.ObserveDuration()
				logQuery("servfail", duration, failingQuery.Reply)
			case q := <-p.inboundQueryChannel:
				if err := q.W.WriteMsg(q.Reply); err != nil {
					Logger.Log(LogMessage{
						Level: CRITICAL,
						Context: LogContext{
							"what":  "failed to write reply to client",
							"error": err.Error(),
							"reply": q.Reply.String(),
						},
					})
				}
				duration := q.Timer.ObserveDuration()
				logQuery(q.Upstream.GetAddress(), duration, q.Reply)
			}
		}
	}()
}

/** generic functions **/

func logCancellation(name string) {
	Logger.Log(LogMessage{
		Level: ERROR,
		Context: LogContext{
			"what":        "cancelling worker thread",
			"thread_name": name,
		},
	})
}
func NewPipelineServerWorker() pipelineServerWorker {
	return pipelineServerWorker{
		//TODO evaluate whether we want these buffered or unbuffered
		inboundQueryChannel:  make(chan Query, 100),
		outboundQueryChannel: make(chan Query, 100),
		failedQueryChannel:   make(chan Query, 100),
		cancelChannel:        make(chan bool),
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

	ret := &PipelineQueryHandler{
		pipelineServerWorker: NewPipelineServerWorker(),
	}

	// INIT PipelineFinisher
	finisherWorker := NewPipelineServerWorker()
	pipelineFinisher := &PipelineFinisher{
		pipelineServerWorker: finisherWorker,
	}

	// handler fails by sending servfails to finisher
	ret.failedQueryChannel = pipelineFinisher.servfailsChannel
	defer pipelineFinisher.Start()

	// INIT PipelineCacher
	cacheWorker := NewPipelineServerWorker()

	// the handler passes good queries to the cacher
	cacheWorker.inboundQueryChannel = ret.outboundQueryChannel
	cache, err := NewCache()
	if err != nil {
		return fmt.Errorf("could not create record cache for PipelineCacher: %s", err.Error())
	}

	cachr := &PipelineCacher{
		pipelineServerWorker: cacheWorker,
		cache:                cache,
		cachingChannel:       make(chan Query, 100),
	}
	defer cachr.Start()

	// INIT connector
	connectorWorker := NewPipelineServerWorker()
	connectorWorker.inboundQueryChannel = cacheWorker.outboundQueryChannel
	connectr := &connector{
		pipelineServerWorker: connectorWorker,
		client:               client,
		connPool:             NewConnPool(),
	}
	for _, name := range config.Upstreams {
		upstream := &Upstream{
			Name: name,
		}
		connectr.AddUpstream(upstream)
	}

	defer connectr.Start()

	// INIT PipelineQuerier
	querierWorker := NewPipelineServerWorker()
	// successful connections from the connector go to the querier
	querierWorker.inboundQueryChannel = connectr.outboundQueryChannel

	// a cache miss also goes to the querier
	cacheWorker.failedQueryChannel = querierWorker.inboundQueryChannel

	// failed queries get retried by the connectr
	querierWorker.failedQueryChannel = connectr.inboundQueryChannel
	PipelineQuerier := &PipelineQuerier{
		pipelineServerWorker: querierWorker,
		client:               client,
	}
	defer PipelineQuerier.Start()
	return nil
}
