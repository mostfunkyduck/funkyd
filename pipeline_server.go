package main

import (
	"fmt"
	"time"

	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
)

// Basic functions for a pipeline worker.
type PipelineServerWorker interface {
	// starts the worker gr(s)
	Start()

	// Dispatches a query to the next step in the pipeline
	Dispatch(q Query)

	// Dispatches a query to be failed
	Fail(q Query)
}

// interface for the timer in the query struct, mainly to avoid
// tightly coupling to prometheus.
type QueryDurationTimer interface {
	ObserveDuration() time.Duration
}

// Basic implementation for a pipeline worker.
type pipelineServerWorker struct {
	PipelineServerWorker
	// channel for accepting new queries
	inboundQueryChannel chan Query

	// channel for dispatching failed queries
	failedQueryChannel chan Query

	// channel for dispatching successful queries
	outboundQueryChannel chan Query

	// channel for hard failing queries
	servfailsChannel chan Query

	// cancel channel - mainly for testing, nobody
	// else is likely to have this channel
	// TODO evaluate some kind of global cancellation
	cancelChannel chan bool
}

/** Worker Implementations **/

// Pairs outbound queries with connections
// Dispatch: connection is successful, forward to PipelineQuerier
// Fail: connection failure, forward to PipelineFinisher for servfail.
type PipelineConnector struct {
	pipelineServerWorker

	// connection pool
	connPool ConnPool

	// Client for making outbound connections
	client Client

	addingChannel chan Query

	closingChannel chan Query
}

// checks queries against the cache
// Dispatch: cache hit - forwards to PipelineQuerier
// Fail: cache miss - forward to PipelineConnector

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
// Fail: send back to PipelineConnector for a new connection,
// this will keep happening until the PipelineConnector gives up.
type PipelineQuerier struct {
	pipelineServerWorker

	// client to send outbound queries with
	client Client

	// send messages here to be cached
	cachingChannel chan Query
}

type PipelineFinisher struct {
	pipelineServerWorker

	// dedicated channel for servfails, this makes
	// the code more readable than just overriding one of the other channels
	servfailsChannel chan Query
}

// Context variable for queries, passed through pipeline to all workers.
type Query struct {
	// The query payload
	Msg *dns.Msg

	// The target upstream
	Upstream Upstream

	// The connection to use
	Conn ConnEntry

	// how many times this query has had to retry a connection
	ConnectionRetries int

	// the prometheus timer to use for this query
	Timer QueryDurationTimer

	// the reply received for this query
	Reply *dns.Msg

	// the response writer to reply on
	W ResponseWriter
}

// base worker functions.
func (p *pipelineServerWorker) Dispatch(q Query) {
	p.outboundQueryChannel <- q
}

func (p *pipelineServerWorker) Fail(q Query) {
	p.failedQueryChannel <- q
}

// PipelineConnector.
func (c *PipelineConnector) AddUpstream(u *Upstream) {
	c.connPool.AddUpstream(u)
}

func (c *PipelineConnector) Start() {
	go func() {
		for {
			select {
			case query := <-c.addingChannel:
				if err := c.connPool.Add(query.Conn); err != nil {
					Logger.Log(LogMessage{
						Level: ERROR,
						Context: LogContext{
							"what":  "error adding connection to pool",
							"error": err.Error(),
							// this is expensive, but it's rare enough that it should be fine
							"query": fmt.Sprintf("%v", query),
						},
					})
				}
			case query := <-c.closingChannel:
				c.connPool.CloseConnection(query.Conn)
			case query := <-c.inboundQueryChannel:
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
					go c.Fail(query)
				}
				// dispatch to PipelineQuerier
				go c.Dispatch(assignedQuery)
			case <-c.cancelChannel:
				logCancellation("PipelineConnector")
				return
			}
		}
	}()
}

func (c *PipelineConnector) AssignConnection(q Query) (assignedQuery Query, err error) {
	assignedQuery = q
	connEntry, upstream := c.connPool.Get()
	if (upstream != Upstream{}) {
		var finalError error
		// we need to make a new connection
		retries := GetConfiguration().UpstreamRetries
		if retries == 0 {
			retries = 2
		}
		for i := assignedQuery.ConnectionRetries; i < retries; i++ {
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

// query handler.
func (q *PipelineQueryHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	q.HandleDNS(w, r)
}

func (q *PipelineQueryHandler) HandleDNS(w ResponseWriter, r *dns.Msg) {
	TotalDNSQueriesCounter.Inc()
	queryTimer := prometheus.NewTimer(QueryTimer)

	QueuedQueriesGauge.Inc()
	// dispatch to PipelineCacher
	q.Dispatch(Query{
		Msg:   r,
		W:     w,
		Timer: queryTimer,
	})
	QueuedQueriesGauge.Dec()
}

// PipelineQuerier.
func (q *PipelineQuerier) Query(qu Query) (query Query, err error) {
	query = qu
	RecursiveQueryCounter.Inc()

	var reply *dns.Msg
	if reply, err = attemptExchange(qu.Msg, query.Conn, q.client); err != nil {
		Logger.Log(NewLogMessage(
			WARNING,
			LogContext{
				"what": "failed exchange with upstreams", "error": err.Error(),
			},
			nil,
		))
		// this connection is tainted, try another one
		query.ConnectionRetries++
		// bail
		return query, fmt.Errorf("failed to exchange query %s: %s", qu.Msg.String(), err)
	}

	query.Reply = reply.Copy()
	query.Reply.SetReply(qu.Msg)
	return query, nil
}

func (q *PipelineQuerier) Start() {
	go func() {
		for {
			select {
			case <-q.cancelChannel:
				logCancellation("PipelineQuerier")
				return
			case query := <-q.inboundQueryChannel:
				// run the actual queries in grs to get maximum throughput - nothing in there should cause contention
				go func() {
					qu, err := q.Query(query)
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
						return
					}
					// dispatch to replier
					go q.Dispatch(qu)
					go func() { q.cachingChannel <- qu }()
				}()
			}
		}
	}()
}

// PipelineCacher.

// checks for a query in the cache, the cache object handles
// expiry.
func (c *PipelineCacher) CheckCache(q Query) (result Response, ok bool) {
	if (q.Msg == nil) || len(q.Msg.Question) < 1 {
		return Response{}, false
	}
	return c.cache.Get(q.Msg.Question[0].Name, q.Msg.Question[0].Qtype)
}

// adds a connection to the cache.
func (c *PipelineCacher) CacheQuery(q Query) {
	question := q.Msg.Question[0]
	r := Response{
		Entry:        *q.Reply,
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
					q.Reply.SetRcode(q.Msg, resp.Entry.Rcode)
					// pass to PipelineQuerier
					go c.Dispatch(q)
					break
				}
				//	pass to PipelineConnector
				go c.Fail(q)
			case q := <-c.cachingChannel:
				if q.Reply == nil {
					Logger.Log(LogMessage{
						Level: ERROR,
						Context: LogContext{
							"what": "attempted to cache empty reply",
							"next": "discarding cache request",
						},
					})
					break
				}
				c.CacheQuery(q)
				// no need to do anything else
			case <-c.cancelChannel:
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
			case <-p.cancelChannel:
				logCancellation("PipelineFinisher")
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
				go p.Fail(q)
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
				if q.Conn != nil {
					// dispatch for readdition into the conn pool
					go p.Dispatch(q)
				}
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
		inboundQueryChannel:  make(chan Query),
		outboundQueryChannel: make(chan Query),
		failedQueryChannel:   make(chan Query),
		servfailsChannel:     make(chan Query),
		cancelChannel:        make(chan bool),
	}
}

// NewPipelineServer Builds all of the pieces of the pipeline server
// returns the pipeline query handler (the first step in this process), a list
// of cancel channels for turning off the workers, and an optional error.
func NewPipelineServer(cl Client, pool ConnPool) (qh PipelineQueryHandler, cancelChannels []chan bool, err error) {
	config := GetConfiguration()
	client := cl
	if client == nil {
		client = BuildClient()
	}

	queryHandler := &PipelineQueryHandler{
		pipelineServerWorker: NewPipelineServerWorker(),
	}

	// INIT PipelineFinisher
	finisherWorker := NewPipelineServerWorker()
	pipelineFinisher := &PipelineFinisher{
		pipelineServerWorker: finisherWorker,
	}
	defer pipelineFinisher.Start()

	// INIT PipelineCacher
	cacheWorker := NewPipelineServerWorker()
	// the handler passes good queries to the cacher
	cache := NewCache()
	cache.StartCleaningCrew()

	cachr := &PipelineCacher{
		pipelineServerWorker: cacheWorker,
		cache:                cache,
		cachingChannel:       make(chan Query),
	}
	defer cachr.Start()

	// INIT PipelineConnector
	if pool == nil {
		pool = NewConnPool()
	}
	connectorWorker := NewPipelineServerWorker()
	connectr := &PipelineConnector{
		pipelineServerWorker: connectorWorker,
		client:               client,
		connPool:             pool,
		addingChannel:        make(chan Query),
		closingChannel:       make(chan Query),
	}

	// the PipelineConnector needs to know about all our upstreams to make connections
	for _, name := range config.Upstreams {
		upstream := &Upstream{
			Name: name,
		}
		connectr.AddUpstream(upstream)
	}

	defer connectr.Start()

	// INIT PipelineQuerier
	querierWorker := NewPipelineServerWorker()
	querier := &PipelineQuerier{
		pipelineServerWorker: querierWorker,
		client:               client,
	}
	defer querier.Start()

	// Now set up the links

	// query handler passes queries to the cacher, fails them to the servfail channel
	queryHandler.failedQueryChannel = pipelineFinisher.servfailsChannel
	queryHandler.outboundQueryChannel = cacheWorker.inboundQueryChannel
	queryHandler.servfailsChannel = pipelineFinisher.servfailsChannel

	// the cache worker passes cached queries to the finisher, cache misses to the PipelineConnector
	cachr.outboundQueryChannel = pipelineFinisher.inboundQueryChannel
	cachr.failedQueryChannel = connectr.inboundQueryChannel
	cachr.servfailsChannel = pipelineFinisher.servfailsChannel

	// fails go to the servfail channel, successful connections go to the querier worker
	connectr.outboundQueryChannel = querier.inboundQueryChannel
	connectr.failedQueryChannel = pipelineFinisher.servfailsChannel
	connectr.servfailsChannel = pipelineFinisher.servfailsChannel

	// failed queries get retried by the connectr
	querier.outboundQueryChannel = pipelineFinisher.inboundQueryChannel
	querier.cachingChannel = cachr.cachingChannel
	querier.failedQueryChannel = connectr.inboundQueryChannel
	querier.servfailsChannel = pipelineFinisher.servfailsChannel

	// the finished dispatched queries back to the connpool for adding
	pipelineFinisher.outboundQueryChannel = connectr.addingChannel
	pipelineFinisher.failedQueryChannel = connectr.closingChannel
	cancelChannels = []chan bool{
		// the query handler is going to keep going until the underlying server stops
		// cancellation is not implemenmted
		// queryHandler.cancelChannel,
		cacheWorker.cancelChannel,
		connectorWorker.cancelChannel,
		querierWorker.cancelChannel,
		pipelineFinisher.cancelChannel,
	}
	return *queryHandler, cancelChannels, nil
}
