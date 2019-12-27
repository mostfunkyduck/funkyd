package main

import (
  "sync"
  "time"
)
type Server struct{
  Cache *RecordCache
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

type Record struct {
  Key           string
  Entry         interface{}
  Ttl           time.Duration
  Qtype         uint16
  CreationTime  time.Time
}
