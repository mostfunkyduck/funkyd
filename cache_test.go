package main

import (
  "time"
  "testing"
  "github.com/miekg/dns"
)

func setupCache(t *testing.T) *RecordCache {
  rc, err := NewCache()
  if err != nil {
    t.Errorf("couldn't initialize new cache: %s\n", err)
  }
  return rc
}

func setupRecord(t *testing.T) *Record {
  return &Record{
    Key: "key",
  }
}

func TestCache(t *testing.T) {
  setupCache(t)
}

func TestStorage(t *testing.T) {
  cache := setupCache(t)
  record := setupRecord(t)
  record.Ttl = 1099 * time.Second
  record.CreationTime = time.Now()
  cache.Add(record)
  arecs, ok := cache.Get(record.Key, record.Qtype)
  if !ok {
    t.Errorf("cache retrieval failed")
    return
  }

  if len(arecs) != 1 {
    t.Errorf("cache retrieval spat back %d records when only one was put in", len(arecs))
    return
  }
  if arecs[0] != record {
    t.Errorf("cache retrieval didn't return record matching what was inserted [%v][%v]\n", record, arecs[0])
  }

  cache.Remove(record)
  newrecord, ok := cache.Get(record.Key, record.Qtype)
  if ok {
    t.Errorf("deletion didn't work: [%v] [%v]\n", cache, newrecord)
  }
}

func TestClean(t *testing.T) {
  cache := setupCache(t)
  record := setupRecord(t)
  record.Ttl = 10 * time.Second
  record.CreationTime = time.Now().Add(-15 * time.Second)
  cache.Add(record)
  _, ok := cache.Get(record.Key, record.Qtype)
  if !ok {
    t.Errorf("failed to add record: [%v][%v]\n", cache, record)
  }
  cache.Clean()
  newrecord, ok := cache.Get("key", dns.TypeA)
  if ok {
    t.Errorf("cleaning didn't work: [%v] [%v]\n", cache, newrecord)
  }
}
