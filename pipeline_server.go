package main

import (
	"fmt"
	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
)

type QueryHandlerWorker interface {
	// starts the worker gr(s)
	Start()

	// Dispatches a query to the next step in the pipeline
	Dispatch(q Query)

	// Dispatches a query to be failed
	Fail(q Query)
}

// checks queries against the cache
type Cacher interface {
	QueryHandlerWorker

	// 
}

// does actual queries
type Querier interface {
	QueryHandlerWorker

	// looks up records
  Query(q Query) (Query, error)
}

// Pairs outbound queries with connections
type Connector interface {
	QueryHandlerWorker

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
	QueryHandlerWorker
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

func (q *querier) Query(qu Query) (query Query, err error) {
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
