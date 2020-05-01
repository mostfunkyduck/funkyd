package main

import (
	"github.com/miekg/dns"
	"sync"
	"time"
)

type LogLevel int

type Server struct {
	// lookup cache
	Cache *RecordCache
	// cache of records hosted by this server
	HostedCache *RecordCache
	// client for recursive lookups
	dnsClient *dns.Client

	// connection cache, b/c whynot
	connPool	ConnPool
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
