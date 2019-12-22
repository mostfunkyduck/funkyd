package main

import (
  "testing"
)

func setupCache(t *testing.T) *RecordCache {
  rc, err := NewCache()
  if err != nil {
    t.Errorf("couldn't initialize new cache: %s\n", err)
  }
  return rc
}

func setupRecord(t *testing.T) *Record {
  return &Record{}
}

func TestCache(t *testing.T) {
  setupCache(t)
}

func TestStorage(t *testing.T) {
  cache := setupCache(t)
  record := setupRecord(t)
  cache.Add("key", record)
  newrecord, _ := cache.Get("key")
  if record != newrecord {
    t.Errorf("cache retrieval didn't return record matching what was inserted [%v][%v]\n", record, newrecord)
  }
}
