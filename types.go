package main

import (
	"github.com/miekg/dns"
	"golang.org/x/sync/semaphore"
	"sync"
	"time"
)

type LogLevel int
type ResolverName string
type Resolver struct {
	Name   ResolverName
	Weight int
}

// making this to support dependency injection into the server
type Client interface {
	Dial(address string) (conn *dns.Conn, err error)
	ExchangeWithConn(s *dns.Msg, conn *dns.Conn) (r *dns.Msg, rtt time.Duration, err error)
}

type Server struct {
	// lookup cache
	Cache *RecordCache
	// cache of records hosted by this server
	HostedCache *RecordCache
	// client for recursive lookups
	dnsClient Client

	// connection cache, b/c whynot
	connPool ConnPool

	// worker pool semaphore
	sem *semaphore.Weighted

	// list of resolvers, to be randomly shuffled
	Resolvers []*Resolver

	RWLock Lock
}

type Lock struct {
	sync.RWMutex
	locklevel int
}

type RecordCache struct {
	cache      map[string]Response
	cleanTimer *time.Timer
	lock       Lock
}

// DNS response cache wrapper
type Response struct {
	Key          string
	Entry        dns.Msg
	Ttl          time.Duration
	Qtype        uint16
	CreationTime time.Time
}

// Cache entry + metadata for record caches
type Record struct {
	Key   string
	Entry interface{}
}
