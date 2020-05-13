package main

import (
	"github.com/miekg/dns"
	"golang.org/x/sync/semaphore"
	"sync"
	"time"
)

type LogLevel int

type UpstreamName string
type UpstreamWeight float64
type Upstream struct {
	// The hostname of the upstream
	Name UpstreamName

	// The port to connect to
	Port int

	// The current weight score of this upstream
	Weight UpstreamWeight
}

// making this to support dependency injection into the server
type Client interface {
	// Make a new connection
	Dial(address string) (conn *dns.Conn, err error)

	// Run DNS queries
	ExchangeWithConn(s *dns.Msg, conn *dns.Conn) (r *dns.Msg, rtt time.Duration, err error)
}

// this abstraction helps us test the entire servedns path
type ResponseWriter interface {
	WriteMsg(*dns.Msg) error
}

type Server interface {
	// Needs to handle DNS queries
	dns.Handler

	// Internal function to implement ServeDNS, this allows testing
	HandleDNS(w ResponseWriter, m *dns.Msg)

	// Retrieves a new connection to an upstream
	GetConnection() (*ConnEntry, error)

	// Runs a recursive query for a given record and record type
	RecursiveQuery(domain string, rrtype uint16) (Response, string, error)

	// Retrieves records from cache or an upstream
	RetrieveRecords(domain string, rrtype uint16) (Response, string, error)

	// Retrieve the server's outbound client
	GetDnsClient() Client

	// Retrieve the cache of locally hosted records
	GetHostedCache() *RecordCache

	// Add a upstream to the server's list
	AddUpstream(u *Upstream)

	// Get a copy of the connection pool for this server
	GetConnectionPool() *ConnPool
}

type ConnPool struct {
	// List of actual upstream structs
	upstreams []*Upstream

	// List of upstream names.  This is kept separate so that callers
	// can iterate through the upstreams list by name without having
	// to lock the actual upstreams array.
	upstreamNames []UpstreamName

	cache map[string][]*ConnEntry
	lock  Lock
}

type CachedConn interface {
	Close() error
}

type ConnEntry struct {
	// The actual connection
	Conn CachedConn

	// The upstream that this connection is associated with
	upstream Upstream

	// The total RTT for this connection
	totalRTT time.Duration

	// The total exchanges for this connection
	exchanges int
}

type MutexServer struct {
	// lookup cache
	Cache *RecordCache

	// cache of records hosted by this server
	HostedCache *RecordCache

	// connection pool
	connPool *ConnPool

	// worker pool semaphore
	sem *semaphore.Weighted

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
