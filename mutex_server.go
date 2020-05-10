package main

// The mutex server uses traditional concurrency controls
import (
	"context"
	"fmt"
	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/semaphore"
	"math/rand"
	"time"
)

func (s *MutexServer) GetConnection(address string) (*ConnEntry, error) {
	connEntry, err := s.connPool.Get(address)
	if err == nil {
		ReusedConnectionsCounter.WithLabelValues(address).Inc()
		Logger.Log(NewLogMessage(
			INFO,
			LogContext{
				"what": "connection pool cache hit",
				"next": "using stored connection",
			},
			"",
		))
		return connEntry, nil
	}
	Logger.Log(NewLogMessage(
		INFO,
		LogContext{
			"what": "connection pool cache miss",
		},
		"",
	))
	return &ConnEntry{}, err
}

func (s *MutexServer) MakeConnection(address string) (*ConnEntry, error) {
	Logger.Log(NewLogMessage(
		INFO,
		LogContext{
			"what": fmt.Sprintf("creating new connection to %s", address),
			"next": "dialing",
		},
		"",
	))

	tlsTimer := prometheus.NewTimer(prometheus.ObserverFunc(func(v float64) {
		TLSTimer.WithLabelValues(address).Observe(v)
	}),
	)
	NewConnectionAttemptsCounter.WithLabelValues(address).Inc()
	conn, err := s.dnsClient.Dial(address)
	tlsTimer.ObserveDuration()
	if err != nil {
		Logger.Log(NewLogMessage(
			ERROR,
			LogContext{
				"what": fmt.Sprintf("error connecting to [%s]: [%s]", address, err),
			},
			"",
		))
		FailedConnectionsCounter.WithLabelValues(address).Inc()
		return &ConnEntry{}, err
	}
	Logger.Log(NewLogMessage(
		INFO,
		LogContext{
			"what": fmt.Sprintf("connection to %s successful", address),
		},
		"",
	))

	return &ConnEntry{Conn: conn, Address: address}, nil
}

func (s *MutexServer) SetResolvers(r []*Resolver) {
	s.Resolvers = r
}

func (s *MutexServer) GetResolverNames() []ResolverName {
	var resolvers []ResolverName
	for _, v := range s.Resolvers {
		resolvers = append(resolvers, v.Name)
	}

	rand.Shuffle(len(resolvers), func(i, j int) {
		resolvers[i], resolvers[j] = resolvers[j], resolvers[i]
	})
	return resolvers
}

func (s *MutexServer) attemptExchange(address string, m *dns.Msg) (ce *ConnEntry, r *dns.Msg, success bool) {
	ce, err := s.GetConnection(address)

	if err != nil {
		Logger.Log(NewLogMessage(
			INFO,
			LogContext{"what": fmt.Sprintf("cache miss connecting to [%s]: [%s]", address, err), "next": "creating new connection"},
			"",
		))
		ce, err = s.MakeConnection(address)
		if err != nil {
			Logger.Log(NewLogMessage(
				ERROR,
				LogContext{
					"what": fmt.Sprintf("error connecting to [%s]: [%s]", address, err),
				},
				"",
			))
			return &ConnEntry{}, &dns.Msg{}, false
		}
	}

	exchangeTimer := prometheus.NewTimer(prometheus.ObserverFunc(func(v float64) {
		ExchangeTimer.WithLabelValues(address).Observe(v)
	}),
	)
	r, _, err = s.dnsClient.ExchangeWithConn(m, ce.Conn.(*dns.Conn))
	exchangeTimer.ObserveDuration()
	if err != nil {
		ce.Conn.Close()
		ResolverErrorsCounter.WithLabelValues(ce.Address).Inc()
		Logger.Log(NewLogMessage(
			ERROR,
			LogContext{
				"what": fmt.Sprintf("error looking up domain [%s] on server [%s]: %s", m.Question[0].Name, address, err),
			},
			"",
		))
		// try the next one
		return &ConnEntry{}, &dns.Msg{}, false
	}
	return ce, r, true
}

func (s *MutexServer) RecursiveQuery(domain string, rrtype uint16) (Response, string, error) {
	RecursiveQueryCounter.Inc()

	// based on example code https://github.com/miekg/dns/blob/master/example_test.go
	port := "853"

	m := &dns.Msg{}
	m.SetQuestion(domain, rrtype)
	m.RecursionDesired = true

	for _, resolver := range s.GetResolverNames() {
		address := string(resolver) + ":" + port

		Logger.Log(NewLogMessage(
			INFO,
			LogContext{
				"what": fmt.Sprintf("need connection to [%s]", address),
				"next": "checking connection pool",
			},
			"",
		))

		config := GetConfiguration()

		// to avoid locals in the loop overriding what we need on the outer level
		// predefine the vars here
		var ce *ConnEntry
		var r *dns.Msg
		var success bool
		for i := 0; i <= config.UpstreamRetries; i++ {
			if ce, r, success = s.attemptExchange(address, m); success {
				break
			}
		}
		if !success {
			// move on to the next resolver
			continue
		}
		added, err := s.connPool.Add(ce)
		if err != nil {
			Logger.Log(NewLogMessage(
				ERROR,
				LogContext{
					"what": fmt.Sprintf("could not add connection entry [%v] to pool!", ce),
					"why":  fmt.Sprintf("%s", err),
					"next": "continuing without cache, disregarding error",
				},
				"",
			))
		}

		if !added {
			Logger.Log(NewLogMessage(
				INFO,
				LogContext{
					"what": fmt.Sprintf("not adding connection to [%s] to pool", ce.Address),
					"why":  "connection pool was full",
					"next": "closing connection",
				},
				"",
			))
			ce.Conn.Close()
		}

		// this one worked, proceeding
		reply, err := processResults(*r, domain, rrtype)
		return reply, ce.Address, err
	}
	// if we went through each server and couldn't find at least one result, bail with the full error string
	return Response{}, "", fmt.Errorf("could not connect to any resolvers")
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
			"",
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
					"what":  fmt.Sprintf("error retrieving record for domain [%s]", domain),
					"error": fmt.Sprintf("%s", err),
					"next":  "returning SERVFAIL",
				},
				fmt.Sprintf("original request [%v]\nresponse: [%v]\n", r, response),
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

func NewMutexServer(cl Client) (Server, error) {
	// seed the random generator once for resolver shuffling
	rand.Seed(time.Now().UnixNano())

	config := GetConfiguration()
	client := cl
	if client == nil {
		var err error
		client, err = BuildClient()
		if err != nil {
			return &MutexServer{}, fmt.Errorf("could not build client [%s]\n", err)
		}
	}

	// TODO this can prbly be simplified
	var c int64
	c = int64(config.ConcurrentQueries)
	if c == 0 {
		c = 10
	}

	sem := semaphore.NewWeighted(c)

	ret := &MutexServer{
		dnsClient: client,
		connPool:  InitConnPool(),
		sem:       sem,
	}
	newcache, err := NewCache()
	if err != nil {
		return nil, fmt.Errorf("couldn't initialize lookup cache: %s\n", err)
	}
	newcache.Init()
	ret.Cache = newcache

	hostedcache, err := NewCache()
	// don't init, we don't clean this one
	ret.HostedCache = hostedcache
	resolverNames := config.Resolvers
	for i, name := range resolverNames {
		ret.Resolvers = append(ret.Resolvers, &Resolver{
			Name: name,
			// start off in order
			Weight: i,
		})
	}
	return ret, nil
}
