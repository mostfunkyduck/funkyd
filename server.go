package main

import (
	"crypto/tls"
	"fmt"
	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
	"strings"
	"time"
)

func sendServfail(w dns.ResponseWriter, duration time.Duration, r *dns.Msg) {
	LocalServfailsCounter.Inc()
	m := &dns.Msg{}
	m.SetRcode(r, dns.RcodeServerFailure)
	w.WriteMsg(m)
	logQuery("servfail", duration, m)
}

func (server *Server) processResults(r dns.Msg, domain string, rrtype uint16) (Response, error) {
	return Response{
		Entry:        r,
		CreationTime: time.Now(),
		Key:          domain,
		Qtype:        rrtype,
	}, nil
}

func (s *Server) GetConnection(address string) (*ConnEntry, error) {
	connEntry, err := s.connPool.Get(address)
	if err == nil {
		ReusedConnectionsCounter.WithLabelValues(address).Inc()
		return connEntry, nil
	}

	Logger.Log(NewLogMessage(
		INFO,
		LogContext {
      "what": fmt.Sprintf("creating new connection to %s", address),
		  "why":  fmt.Sprintf("error [%s]", err),
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
		return &ConnEntry{}, err
	}
	Logger.Log(NewLogMessage(
		DEBUG,
		LogContext {
      "what": fmt.Sprintf("connection to %s successful", address),
    },
		fmt.Sprintf("%v\n", conn),
	))

	return &ConnEntry{Conn: conn, Address: address}, nil
}

func (s *Server) RecursiveQuery(domain string, rrtype uint16) (Response, string, error) {
	RecursiveQueryCounter.Inc()
	// lifted from example code https://github.com/miekg/dns/blob/master/example_test.go
	port := "853"
	// TODO error checking
	config := GetConfiguration()

	m := &dns.Msg{}
	m.SetQuestion(domain, rrtype)
	m.RecursionDesired = true
	for _, host := range config.Resolvers {
		address := host + ":" + port
		Logger.Log(NewLogMessage(
			INFO,
			LogContext {
        "what": fmt.Sprintf("attempting connection to [%s]\n", address),
			  "next": "checking connection pool",
      },
			"",
		))
		ce, err := s.GetConnection(address)
		if err != nil {
			return Response{}, "", err
		}
		r, _, err := s.dnsClient.ExchangeWithConn(m, ce.Conn)

		if err != nil {
			ResolverErrorsCounter.WithLabelValues(ce.Address).Inc()
			Logger.Log(NewLogMessage(
				ERROR,
				LogContext {
          "what": fmt.Sprintf("error looking up domain [%s] on server [%s:%s]: %s", domain, address, port, err),
				  "next": "continuing to next resolver",
        },
				"",
			))
			// try the next one
			continue
		}

		added, err := s.connPool.Add(ce)
		if err != nil {
			Logger.Log(NewLogMessage(
				ERROR,
				LogContext {
          "what": fmt.Sprintf("could not add connection entry [%v] to pool!", ce),
				  "why": fmt.Sprintf("%s", err),
				  "next": "continuing without cache, disregarding error",
        },
				fmt.Sprintf("server: [%v]", s),
			))
		}

		if !added {
			Logger.Log(NewLogMessage(
				INFO,
        LogContext {
          "what": fmt.Sprintf("not adding connection to [%s] to pool", ce.Address),
          "why": "connection pool was full",
          "next": "closing connection",
        },
				fmt.Sprintf("connEntry: [%v]", ce),
			))
			ce.Conn.Close()
		}

		// this one worked, proceeding
		reply, err := s.processResults(*r, domain, rrtype)
		return reply, ce.Address, err
	}
	// if we went through each server and couldn't find at least one result, bail with the full error string
	return Response{}, "", fmt.Errorf("could not connect to any resolvers")
}

// retrieves the record for that domain, either from cache or from
// a recursive query
func (server *Server) RetrieveRecords(domain string, rrtype uint16) (Response, string, error) {
	// First: check caches

	// Need to keep caches locked until the end of the function because
	// we may need to add records consistently
	cached_response, ok := server.Cache.Get(domain, rrtype)
	if ok {
		CacheHitsCounter.Inc()
		return cached_response, "cache", nil
	}

	// Now check the hosted cache (stuff in our zone files that we're taking care of
	cached_response, ok = server.HostedCache.Get(domain, rrtype)
	if ok {
		HostedCacheHitsCounter.Inc()
		return cached_response, "cache", nil
	}

	// Second, query upstream if there's no cache
	// TODO only do if requested b/c thats what the spec says IIRC
	response, source, err := server.RecursiveQuery(domain, rrtype)
	if err != nil {
		return response, "", fmt.Errorf("error running recursive query on domain [%s]: %s\n", domain, err)
	}
	server.Cache.Add(response)
	return response, source, nil
}
func logQuery(source string, duration time.Duration, response *dns.Msg) error {
	// queried domain, query type, opcode,	response, authoritative NS
  var context LogContext
	for i, _ := range response.Question {
		for j, _ := range response.Answer {
			answerBits := strings.Split(response.Answer[j].String(), " ")
			context = LogContext {
				"name": response.Question[i].Name,
				"type": dns.Type(response.Question[i].Qtype).String(),
				"opcode": dns.OpcodeToString[response.Opcode],
				"answer": answerBits[len(answerBits)-1],
				"answerSource": fmt.Sprintf("[%s]", source),
				"duration": fmt.Sprintf("%s", duration),
      }
			QueryLogger.Log(NewLogMessage(
				CRITICAL,
        context,
        "",
			))
		}
	}
	return nil
}

func (s *Server) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	TotalDnsQueriesCounter.Inc()
	queryTimer := prometheus.NewTimer(QueryTimer)

	msg := dns.Msg{}
	msg.SetReply(r)
	domain := msg.Question[0].Name
	// FIXME when should this be set
	msg.Authoritative = false
	msg.RecursionAvailable = true

	go func() {
		response, source, err := s.RetrieveRecords(domain, r.Question[0].Qtype)
		duration := queryTimer.ObserveDuration()
		if err != nil {
			Logger.Log(NewLogMessage(
				ERROR,
        LogContext {
          "what": fmt.Sprintf("error retrieving record for domain [%s]", domain),
          "why": fmt.Sprintf("%s", err),
          "next": "returning SERVFAIL",
        },
        fmt.Sprintf("original request [%v]\nresponse: [%v]\n", r, response),
			))
			sendServfail(w, duration, r)
			return
		}

		reply := response.Entry.Copy()
		// this calls reply.SetReply() as well, correctly configuring all the metadata
		reply.SetRcode(r, response.Entry.Rcode)
		w.WriteMsg(reply)
		logQuery(source, duration, reply)
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
    LogContext {
      "what": "instantiated new dns client in TLS mode",
		  "why": "",
		  "next": "returning for use",
    },
		fmt.Sprintf("%v\n", cl),
	))
	return cl, nil
}

func NewServer() (*Server, error) {
	client, err := buildClient()
	if err != nil {
		return &Server{}, fmt.Errorf("could not build client [%s]\n", err)
	}
	ret := &Server{
		dnsClient: client,
		connPool:  InitConnPool(),
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
	return ret, nil
}
