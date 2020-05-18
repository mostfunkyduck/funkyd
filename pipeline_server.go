package main

import (
	"fmt"
	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
)

type PipelineServerWorker interface {
	// starts the worker gr(s)
	Start()

	// Dispatches a query to the next step in the pipeline
	Dispatch(q Query)

	// Dispatches a query to be failed
	Fail(q Query)
}

// checks queries against the cache
type Cacher interface {
	PipelineServerWorker

	// determines if this query is cached
	IsCached(q Query) (ConnEntry, bool)
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

type Query struct {
	// The query payload
	Msg *dns.Msg

	// The target upstream
	Upstream Upstream

	// The connection to use
	Conn *ConnEntry

	// the prometheus timer to use for this query
	Timer	*prometheus.Timer

	// the reply received for this query
	Reply	*dns.Msg
}


type connector struct {
	// channel for accepting new queries
	connectionChannel chan Query

	// channel for dispatching failed queries
	failedQueriesChannel	chan Query

	// channel for dispatching successful queries
	successfulQueriesChannel	chan Query

	// connection pool
	connPool ConnPool

	// Client for making outbound connections
	client Client
}

// handles initial connection
type QueryHandler struct {
	PipelineServerWorker
	connectionChannel chan Query
}
type querier struct {
	// channel to receive queries on
	queryChannel	chan Query

	// channel to dispatch failed queries to
	failedQueriesChannel chan Query

	// channel to dispatch successful queries to
	successfulQueriesChannel chan Query
}

func (c *connector) AddUpstream(u *Upstream) {
	c.connPool.AddUpstream(u)
}

func (c connector) Start() {
	go func() {
		for query := range c.connectionChannel {
			assignedQuery, err := c.AssignConnection(query)
			if err != nil {
				Logger.Log(LogMessage {
					Level: ERROR,
					Context: LogContext {
						"what": "connection manager failed to assign connection to query",
						"query": assignedQuery.Msg.String(),
						"next": "dispatching to be SERVFAILed",
					},
				})
				c.Fail(assignedQuery)
			}
			c.DispatchQuery(assignedQuery)
		}
	}()
}

func (c *connector) DispatchQuery (q Query) {
	c.successfulQueriesChannel <- q
}

func (c *connector) Fail(q Query) {
	c.failedQueriesChannel <- q
}

func (c *connector) AssignConnection(q Query) (assignedQuery Query, err error) {
	assignedQuery = q
	connEntry, upstream := c.connPool.Get()
	if (upstream != Upstream{}) {
		// we need to make a new connection
		connEntry, err = c.connPool.NewConnection(upstream, c.client.Dial)
		if err != nil {
			Logger.Log(LogMessage {
				Level: WARNING,
				Context: LogContext {
					"what": "failed to make connection to upstream",
					"address": upstream.GetAddress(),
					"upstream": Logger.Sprintf(DEBUG, "%v", upstream),
				},
			})
			return Query{}, err
		}
	}
	assignedQuery.Conn = connEntry
	return assignedQuery, nil
}
func (s *QueryHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	s.HandleDNS(w, r)
}

func (s *QueryHandler) HandleDNS(w dns.ResponseWriter, r *dns.Msg) {
	//s.resolverChannel <- query
	TotalDnsQueriesCounter.Inc()
	// we got this query, but it isn't getting handled until we get the sem
	QueuedQueriesGauge.Inc()
	queryTimer := prometheus.NewTimer(QueryTimer)

	s.connectionChannel <- Query {
		Msg: r,
		Timer: queryTimer,
	}
	QueuedQueriesGauge.Dec()
}


func (q *querier) Fail(qu Query) {
	q.failedQueriesChannel <- qu
}

func (q *querier) Dispatch(qu Query) {
	q.successfulQueriesChannel <- qu
}

// assumes that the caller will close connection upon any errors
func attemptExchange(m *dns.Msg, ce *ConnEntry, client Client) (reply *dns.Msg, err error) {
	address := ce.GetAddress()
	exchangeTimer := prometheus.NewTimer(prometheus.ObserverFunc(func(v float64) {
		ExchangeTimer.WithLabelValues(address).Observe(v)
	}),
	)
	reply, rtt, err := client.ExchangeWithConn(m, ce.Conn.(*dns.Conn))
	exchangeTimer.ObserveDuration()
	ce.AddExchange(rtt)
	if err != nil {
		UpstreamErrorsCounter.WithLabelValues(address).Inc()
		Logger.Log(NewLogMessage(
			ERROR,
			LogContext{
				"what":  fmt.Sprintf("error looking up domain [%s] on server [%s]", m.Question[0].Name, address),
				"error": fmt.Sprintf("%s", err),
			},
			func() string { return fmt.Sprintf("request [%v]", m) },
		))
		// try the next one
		return &dns.Msg{}, err
	}
	return reply, nil
}

func (q *querier) Query(qu Query) (query Query, err error) {
	RecursiveQueryCounter.Inc()

	m := &dns.Msg{}
	m.SetQuestion(qu.Msg.Question[0].Name, qu.Msg.Question[0].Qtype)
	m.RecursionDesired = true

	config := GetConfiguration()

	for i := 0; i <= config.UpstreamRetries; i++ {
		if r, err = attemptExchange(m, qu.Conn, q.client); err == nil {
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
			ERROR,
			LogContext{
				"what":    "failed to complete any exchanges with upstreams",
				"error":   err.Error(),
				"note":    "this is the most recent error, other errors may have been logged during the failed attempt(s)",
				"address": domain,
				"rrtype":  string(rrtype),
				"next":    "aborting query attempt",
			},
			nil,
		))
		return Response{}, "", fmt.Errorf("failed to complete any exchanges with upstreams: %s", err)
	}

	if err := s.connPool.Add(ce); err != nil {
		Logger.Log(NewLogMessage(
			ERROR,
			LogContext{
				"what":  "could not add connection entry to pool (enable debug logging for variable value)!",
				"error": err.Error(),
				"next":  "continuing without cache, disregarding error",
			},
			func() string { return fmt.Sprintf("ce: [%v]", ce) },
		))
	}

	// this one worked, proceeding
	reply, err := processResults(*r, domain, rrtype)
	return reply, ce.GetAddress(), err
	return qu, nil
}
func (q *querier) Start() {

	go func() {
		for query := range q.queryChannel	{
			query, err := q.Query(query)
			if err != nil {
				Logger.Log(LogMessage {
					Level: ERROR,
					Context: LogContext{
						"what":   "error retrieving record for domain",
						"query": query.Msg.String(),
						"error":  err.Error(),
						"next":   "failing query",
					},
				})
				q.Fail(query)
				continue
			}
			q.Dispatch(query)
		}
	}()
}

func NewQueryHandler(cl Client, pool ConnPool, cm Connector) (err error) {
	config := GetConfiguration()
	client := cl
	if client == nil {
		var err error
		client, err = BuildClient()
		if err != nil {
			return fmt.Errorf("could not build client [%s]", err.Error())
		}
	}

	ret := &QueryHandler {
		connectionChannel: make(chan Query),
	}

	if cm == nil {
		cm := &connector {
			client:	client,
			connPool:	NewConnPool(),
			failedQueriesChannel: make(chan Query),
			connectionChannel: ret.connectionChannel,
			successfulQueriesChannel: make(chan Query),
		}
		for _, name := range config.Upstreams {
			upstream := &Upstream{
				Name: name,
			}
			cm.AddUpstream(upstream)
		}

		cm.Start()
	}
	return nil
}
