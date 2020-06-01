package main

import (
	"time"

	"github.com/miekg/dns"
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

// PipelineCacher.

// checks for a query in the cache, the cache object handles
// expiry.
func (c *PipelineCacher) CheckCache(q Query) (result Response, ok bool) {
	if (q.Msg == nil) || len(q.Msg.Question) < 1 {
		return Response{}, false
	}
	return c.cache.Get(q.Msg.Question[0].Name, q.Msg.Question[0].Qtype)
}

// adds a query to the cache.
func (c *PipelineCacher) CacheQuery(q Query) {
	question := q.Msg.Question[0]
	ttl := uint32(0)
	if len(q.Reply.Answer) > 0 {
		ttl = q.Reply.Answer[0].Header().Ttl
	}
	r := Response{
		Ttl:          time.Duration(ttl) * time.Second,
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
