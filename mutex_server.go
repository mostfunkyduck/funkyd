package main

// The mutex server uses traditional concurrency controls
import (
	"runtime"
	"context"
	"fmt"
	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/semaphore"
)

func (s *MutexServer) newConnection(upstream Upstream) (ce *ConnEntry, err error) {
	// we're supposed to connect to this upstream, no existing connections
	// (this doesn't block)
	ce, err = s.connPool.NewConnection(upstream, s.dnsClient.Dial)
	if err != nil {
		// leaving this at DEBUG since we're passing the actual error up
		address := upstream.GetAddress()
		Logger.Log(NewLogMessage(
			DEBUG,
			LogContext{
				"error":   err.Error(),
				"what":    "could not make new connection to upstream",
				"address": address,
			},
			func() string { return fmt.Sprintf("upstream:[%v]", upstream) },
		))
		return &ConnEntry{}, fmt.Errorf("could not connect to upstream (%s): %s", address, err.Error())
	}
	return
}

func (s *MutexServer) GetConnection() (ce *ConnEntry, err error) {
	// There are 3 cases: cache miss, cache hit, and error
	// responses:
	// 	cache miss, no error: attempt to make a new connection
	//  cache hit: return the conn entry
	//  error: return the error and an empty conn entry
	// first check the conn pool (this blocks)
	ce, upstream, err := s.connPool.Get()
	if err == nil && (upstream != Upstream{}) {
		// cache miss, no error
		Logger.Log(NewLogMessage(
			INFO,
			LogContext{
				"what":    "creating new connection",
				"address": upstream.GetAddress(),
			},
			func() string { return fmt.Sprintf("upstream [%v]", upstream) },
		))

		if ce, err = s.newConnection(upstream); err != nil {
			return &ConnEntry{}, err
		}
	} else if err != nil {
		// error
		return &ConnEntry{}, err
	}

	// cache hit
	address := ce.GetAddress()
	ReusedConnectionsCounter.WithLabelValues(address).Inc()
	Logger.Log(NewLogMessage(
		INFO,
		LogContext{
			"what":    "got connection to from connection pool",
			"address": address,
			"next":    "using stored connection",
		},
		nil,
	))
	return ce, nil
}

func (s *MutexServer) AddUpstream(r *Upstream) {
	s.connPool.AddUpstream(r)
}

func (s *MutexServer) attemptExchange(m *dns.Msg) (ce *ConnEntry, reply *dns.Msg, err error) {
	ce, err = s.GetConnection()
	if err != nil {
		Logger.Log(NewLogMessage(
			ERROR,
			LogContext{
				"what": "error getting connection from pool",
				"error": err.Error(),
				"next":  "aborting exchange attempt"},
			nil,
		))
		return
	}

	address := ce.GetAddress()
	exchangeTimer := prometheus.NewTimer(prometheus.ObserverFunc(func(v float64) {
		ExchangeTimer.WithLabelValues(address).Observe(v)
	}),
	)
	reply, _, err = s.dnsClient.ExchangeWithConn(m, ce.Conn.(*dns.Conn))
	exchangeDuration := exchangeTimer.ObserveDuration()
	ce.AddExchange(exchangeDuration)
	if err != nil {
		s.connPool.CloseConnection(ce)
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
		return &ConnEntry{}, &dns.Msg{}, err
	}
	// just in case something changes above and it reaches this success code with a non-nil error :P
	err = nil
	return
}

func (s *MutexServer) RecursiveQuery(domain string, rrtype uint16) (resp Response, address string, err error) {
	RecursiveQueryCounter.Inc()

	m := &dns.Msg{}
	m.SetQuestion(domain, rrtype)
	m.RecursionDesired = true

	config := GetConfiguration()

	// to avoid locals in the loop overriding what we need on the outer level
	// predefine the vars here
	var ce *ConnEntry
	var r *dns.Msg
	for i := 0; i <= config.UpstreamRetries; i++ {
		if ce, r, err = s.attemptExchange(m); err == nil {
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
				"what":      "failed to complete any exchanges with upstreams",
				"error":     err.Error(),
				"note": "this is the most recent error, other errors may have been logged during the failed attempt(s)",
				"address":   domain,
				"rrtype":    string(rrtype),
				"next":      "aborting query attempt",
			},
			nil,
		))
		return Response{}, "", fmt.Errorf("failed to complete any exchanges with upstreams: %s", err)
	}

	if err := s.connPool.Add(ce); err != nil {
		Logger.Log(NewLogMessage(
			ERROR,
			LogContext{
				"what": "could not add connection entry to pool (enable debug logging for variable value)!",
				"error": err.Error(),
				"next": "continuing without cache, disregarding error",
			},
			func() string { return fmt.Sprintf("ce: [%v]", ce) },
		))
	}

	// this one worked, proceeding
	reply, err := processResults(*r, domain, rrtype)
	return reply, ce.GetAddress(), err
}

// retrieves the record for that domain, either from cache or from
// a recursive query
func (s *MutexServer) RetrieveRecords(domain string, rrtype uint16) (Response, string, error) {
	// First: check caches

	cached_response, ok := s.Cache.Get(domain, rrtype)
	if ok {
		CacheHitsCounter.Inc()
		return cached_response, "cache", nil
	}

	// Now check the hosted cache (stuff in our zone files that we're taking care of)
	cached_response, ok = s.GetHostedCache().Get(domain, rrtype)
	if ok {
		HostedCacheHitsCounter.Inc()
		return cached_response, "cache", nil
	}

	// Next , query upstream if there's no cache
	// TODO only do if requested b/c thats what the spec says IIRC
	response, source, err := s.RecursiveQuery(domain, rrtype)
	if err != nil {
		return response, "", fmt.Errorf("error running recursive query on domain [%s]: %s\n", domain, err)
	}
	s.Cache.Add(response)
	return response, source, nil
}

func (s *MutexServer) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	s.HandleDNS(w, r)
}

func (s *MutexServer) HandleDNS(w ResponseWriter, r *dns.Msg) {
	TotalDnsQueriesCounter.Inc()
	// we got this query, but it isn't getting handled until we get the sem
	QueuedQueriesGauge.Inc()
	queryTimer := prometheus.NewTimer(QueryTimer)

	msg := dns.Msg{}
	msg.SetReply(r)
	domain := msg.Question[0].Name
	// FIXME when should this be set
	msg.Authoritative = false
	msg.RecursionAvailable = true

	ctx := context.TODO()
	if err := s.sem.Acquire(ctx, 1); err != nil {
		Logger.Log(NewLogMessage(
			CRITICAL,
			LogContext{
				"what": "failed to acquire semaphore allowing queries to progress",
				"why":  fmt.Sprintf("%s", err),
				"next": "panicking",
			},
			nil,
		))
		panic(err)
	}
	go func() {
		defer s.sem.Release(1)
		// the query is now in motion, no longer queued
		QueuedQueriesGauge.Dec()
		response, source, err := s.RetrieveRecords(domain, r.Question[0].Qtype)
		if err != nil {
			Logger.Log(NewLogMessage(
				ERROR,
				LogContext{
					"what":  "error retrieving record for domain",
					"domain": domain,
					"error": err.Error(),
					"next":  "returning SERVFAIL",
				},
				func() string { return fmt.Sprintf("original request [%v]\nresponse: [%v]\n", r, response) },
			))
			duration := queryTimer.ObserveDuration()
			sendServfail(w, duration, r)
			return
		}

		reply := response.Entry.Copy()
		// this calls reply.SetReply() as well, correctly configuring all the metadata
		reply.SetRcode(r, response.Entry.Rcode)
		w.WriteMsg(reply)
		duration := queryTimer.ObserveDuration()
		logQuery(source, duration, reply)
	}()
	return
}

func (s *MutexServer) GetDnsClient() Client {
	return s.dnsClient
}

func (s *MutexServer) GetHostedCache() *RecordCache {
	return s.HostedCache
}

func (s *MutexServer) GetConnectionPool() (pool *ConnPool) {
	return s.connPool
}

func NewMutexServer(cl Client, pool *ConnPool) (Server, error) {
	// seed the random generator once for upstream shuffling

	config := GetConfiguration()
	client := cl
	if client == nil {
		var err error
		client, err = BuildClient()
		if err != nil {
			return &MutexServer{}, fmt.Errorf("could not build client [%s]", err.Error())
		}
	}

	var c int64
	if c = int64(config.ConcurrentQueries); c == 0 {
		c = int64(runtime.GOMAXPROCS(0))
	}

	Logger.Log(NewLogMessage(
		INFO,
		LogContext {
			"what": "creating server worker pool",
			"concurrency": string(c),
		},
		nil,
	))

	sem := semaphore.NewWeighted(c)

	if pool == nil {
		pool = NewConnPool()
	}

	newcache, err := NewCache()
	if err != nil {
		return nil, fmt.Errorf("couldn't initialize lookup cache: %s", err)
	}

	hostedcache, err := NewCache()
	if err != nil {
		return nil, fmt.Errorf("couldn't initialize hosted cache: %s", err)
	}

	ret := &MutexServer{
		Cache: newcache,
		HostedCache: hostedcache,
		dnsClient: client,
		connPool:  pool,
		sem:       sem,
	}

	upstreamNames := config.Upstreams
	for _, name := range upstreamNames {
		ret.AddUpstream(&Upstream{
			Name: name,
		})
	}
	return ret, nil
}
