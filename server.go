package main

import (
	"crypto/tls"
	"fmt"
	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
	"time"
)

// convenience function to generate an nxdomain response
func sendNXDomain(w dns.ResponseWriter, r *dns.Msg) {
	NXDomainCounter.Inc()
	m := &dns.Msg{}
	m.SetRcode(r, dns.RcodeNameError)
	w.WriteMsg(m)
}

func sendServfail(w dns.ResponseWriter, r *dns.Msg) {
	LocalServfailsCounter.Inc()
	m := &dns.Msg{}
	m.SetRcode(r, dns.RcodeServerFailure)
	w.WriteMsg(m)
}

func (server *Server) processResults(r dns.Msg, domain string, rrtype uint16) (Response, error) {
	if r.Rcode != dns.RcodeSuccess {
		// wish we had an easier way of translating the rrtype into human, but i don't got one yet
		return Response{}, fmt.Errorf("got unsuccessful lookup code for domain [%s] rrtype [%d]: [%d]", domain, rrtype, r.Rcode)
	}
	return Response{
		Entry:        r,
		CreationTime: time.Now(),
		Key:          domain,
		Qtype:        rrtype,
	}, nil
}

// TODO link that project which sorta inspired this
type ConnPool struct {
	cache map[string][]*ConnEntry
	lock  Lock
}

// idk what it is yet, but if i don't have to store metadata here, i'll be
// surprised af
type ConnEntry struct {
	Conn    *dns.Conn
	Address string
}

func (c *ConnPool) Lock() {
	c.lock.Lock()
}

func (c *ConnPool) Unlock() {
	c.lock.Unlock()
}

// adds a connection to the cache, returns the entry wrapper
func (c *ConnPool) Add(ce *ConnEntry) error {
	c.Lock()
	defer c.Unlock()
	if _, ok := c.cache[ce.Address]; ok {
		c.cache[ce.Address] = append(c.cache[ce.Address], ce)
	} else {
		c.cache[ce.Address] = []*ConnEntry{ce}
	}
	return nil
}

func (c *ConnPool) Get(address string) (*ConnEntry, error) {
	c.Lock()
	defer c.Unlock()
	var ret *ConnEntry
	// Check for an existing connection
	if conns, ok := c.cache[address]; ok {
		if len(conns) > 0 {
			// pop off a connection and return it
			ret, c.cache[address] = conns[0], conns[1:]
			return ret, nil
		}
	}
	return &ConnEntry{}, fmt.Errorf("could not retrieve connection for [%s] from cache", address)
}

func (s *Server) GetConnection(address string) (*ConnEntry, error) {
	connEntry, err := s.connPool.Get(address)
	if err == nil {
		ReusedConnectionsCounter.Inc()
		return connEntry, nil
	}

	Logger.Log(NewLogMessage(
		INFO,
		fmt.Sprintf("creating new connection to %s", address),
		fmt.Sprintf("error [%s]", err),
		"dialing",
		"",
	))

	tlsTimer := prometheus.NewTimer(TLSTimer)
	NewConnectionAttemptsCounter.Inc()
	conn, err := s.dnsClient.Dial(address)
	Logger.Log(NewLogMessage(DEBUG, fmt.Sprintf("connection took [%s]\n", tlsTimer.ObserveDuration()), "", "", ""))
	if err != nil {
		return &ConnEntry{}, err
	}
	Logger.Log(NewLogMessage(
		DEBUG,
		fmt.Sprintf("connection to %s successful", address),
		"no error returned",
		"",
		fmt.Sprintf("%v\n", conn),
	))

	return connEntry, nil
}

func (s *Server) RecursiveQuery(domain string, rrtype uint16) (Response, error) {
	RecursiveQueryCounter.Inc()
	// lifted from example code https://github.com/miekg/dns/blob/master/example_test.go
	port := "853"
	// TODO error checking
	config := GetConfiguration()

	m := &dns.Msg{}
	m.SetQuestion(domain, rrtype)
	m.RecursionDesired = true
	// TODO cycle through all servers. cache connection
	var errorstring string
	for _, host := range config.Resolvers {
		address := host + ":" + port
		Logger.Log(NewLogMessage(
			INFO,
			fmt.Sprintf("attempting connection to [%s]\n", address),
			"",
			"checking connection pool",
			fmt.Sprintf("server: [%v], msg [%v]", s, m),
		))
		ce, err := s.GetConnection(address)
		if err != nil {
			return Response{}, err
		}
		// TODO the new function shouldn't need a host/port
		r, _, err := s.dnsClient.ExchangeWithConn(m, address, ce.Conn)
		if err != nil {
			e := fmt.Errorf("could not execute query msg [%v] against server [%s] using connection [%v]", m, address, ce)
			return Response{}, e
		}

		err = s.connPool.Add(ce)
		if err != nil {
			Logger.Log(NewLogMessage(
				ERROR,
				fmt.Sprintf("could not add connection entry [%v] to pool [%v]!", ce, s.connPool),
				fmt.Sprintf("%s", err),
				"continuing without cache, disregarding error",
				fmt.Sprintf("server: [%v]", s),
			))
		}

		if err != nil {
			ResolverErrorsCounter.Inc()
			// build a huge error string of all errors from all servers
			errorstring = fmt.Sprintf("error looking up domain [%s] on server [%s:%s]: %s: %s", domain, address, port, err, errorstring)
			// try the next one
			continue
		}
		// this one worked, proceeding
		return s.processResults(*r, domain, rrtype)
	}
	// if we went through each server and couldn't find at least one result, bail with the full error string
	return Response{}, fmt.Errorf(errorstring)
}

// retrieves the record for that domain, either from cache or from
// a recursive query
func (server *Server) RetrieveRecords(domain string, rrtype uint16) (Response, error) {
	// First: check caches

	// Need to keep caches locked until the end of the function because
	// we may need to add records consistently
	cached_response, ok := server.Cache.Get(domain, rrtype)
	if ok {
		CacheHitsCounter.Inc()
		return cached_response, nil
	}

	// Now check the hosted cache (stuff in our zone files that we're taking care of
	cached_response, ok = server.HostedCache.Get(domain, rrtype)
	if ok {
		HostedCacheHitsCounter.Inc()
		return cached_response, nil
	}

	// Second, query upstream if there's no cache
	// TODO only do if requested b/c thats what the spec says IIRC
	response, err := server.RecursiveQuery(domain, rrtype)
	if err != nil {
		return response, fmt.Errorf("error running recursive query on domain [%s]: %s\n", domain, err)
	}
	server.Cache.Add(response)
	return response, nil
}

func (server *Server) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	TotalDnsQueriesCounter.Inc()
	queryTimer := prometheus.NewTimer(QueryTimer)

	msg := dns.Msg{}
	msg.SetReply(r)
	domain := msg.Question[0].Name
	// FIXME when should this be set
	msg.Authoritative = false
	msg.RecursionAvailable = true

	go func() {
		defer queryTimer.ObserveDuration()
		response, err := server.RetrieveRecords(domain, r.Question[0].Qtype)
		if err != nil {
			Logger.Log(NewLogMessage(
				WARNING,
				fmt.Sprintf("error retrieving record for domain [%s]", domain),
				fmt.Sprintf("%s", err),
				"returning SERVFAIL",
				fmt.Sprintf("original request [%v]\nresponse: [%v]\n", r, response),
			))
			sendServfail(w, r)
			return
		}

		msg.Answer = response.Entry.Answer

		w.WriteMsg(&msg)
	}()
}

func buildClient() (*dns.Client, error) {
	cl := &dns.Client{
		SingleInflight: true,
		Net:            "tcp-tls",
		TLSConfig:      &tls.Config{},
	}
	Logger.Log(NewLogMessage(
		INFO,
		"instantiated new dns client in TLS mode",
		"",
		"returning for use",
		fmt.Sprintf("%v\n", cl),
	))
	return cl, nil
}

func NewServer() (*Server, error) {
	client, err := buildClient()
	if err != nil {
		return &Server{}, fmt.Errorf("could not build client [%s]\n", err)
	}
	ret := &Server{dnsClient: client, connPool: ConnPool{cache: make(map[string][]*ConnEntry)}}
	newcache, err := NewCache()
	if err != nil {
		return nil, fmt.Errorf("couldn't initialize lookup cache: %s\n", err)
	}
	newcache.Init()
	ret.Cache = newcache

	hostedcache, err := NewCache()
	// don't init, we don't clean this one
	ret.HostedCache = hostedcache
	return ret, nil
}
