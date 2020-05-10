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
	// Make a new connection
	Dial(address string) (conn *dns.Conn, err error)

	// Run DNS queries
	ExchangeWithConn(s *dns.Msg, conn *dns.Conn) (r *dns.Msg, rtt time.Duration, err error)
}

type Server interface {
	// Needs to handle DNS queries
	dns.Handler

	// Retrieves a new connection to an upstream
	GetConnection(address string) (*ConnEntry, error)

	// Makes a new connection to an upstream
	MakeConnection(address string) (*ConnEntry, error)

	// Retrieves a list of resolver names to connect to
	GetResolvers() []ResolverName

	// Runs a recursive query for a given record and record type
	RecursiveQuery(domain string, rrtype uint16) (Response, string, error)

	// Retrieves records from cache or an upstream
	RetrieveRecords(domain string, rrtype uint16) (Response, string, error)

	// Retrieve the server's outbound client
	GetDnsClient() Client

	GetHostedCache() *RecordCache
}

type MutexServer struct {
	// lookup cache
	Cache *RecordCache
	// cache of records hosted by this server
	HostedCache *RecordCache

	// connection cache, b/c whynot
	connPool ConnPool

	// worker pool semaphore
	sem *semaphore.Weighted

	// list of resolvers, to be randomly shuffled
	Resolvers []*Resolver

	// client for recursive lookups
	dnsClient Client

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
