package main

import (
  "github.com/miekg/dns"
  "sync"
  "time"
)
type Server struct{
  // lookup cache
  Cache       *RecordCache
  // cache of records hosted by this server
  HostedCache *RecordCache
}

type Lock struct {
    sync.RWMutex
    locklevel int
}

type RecordCache struct {
  cache map[string][]*Record
  cleanTimer *time.Timer
  lock Lock
}


// Cache entry + metadata for record caches
type Record struct {
  Key           string
  Entry         interface{}
  Ttl           time.Duration
  Qtype         uint16
  CreationTime  time.Time
}
