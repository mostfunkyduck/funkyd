package main

import (
	"github.com/google/go-cmp/cmp"
	"github.com/mostfunkyduck/dns"
	"testing"
	"time"
)

func setupCache(t *testing.T) *RecordCache {
	rc, err := NewCache()
	if err != nil {
		t.Errorf("couldn't initialize new cache: %s\n", err)
	}
	return rc
}

func setupResponse(t *testing.T) Response {
	return Response{
		Key:   "key",
		Entry: dns.Msg{},
	}
}

func TestCache(t *testing.T) {
	setupCache(t)
}

func TestStorage(t *testing.T) {
	cache := setupCache(t)
	response := setupResponse(t)
	response.Ttl = 1099 * time.Second
	response.CreationTime = time.Now()
	cache.Add(response)
	cachedResponse, ok := cache.Get(response.Key, response.Qtype)
	if !ok {
		t.Errorf("cache retrieval failed")
		return
	}

	if !cmp.Equal(cachedResponse, response) {
		t.Errorf("cache retrieval didn't return record matching what was inserted [%v][%v]\n", cachedResponse, response)
	}

	cache.Remove(response)
	newrecord, ok := cache.Get(response.Key, response.Qtype)
	if ok {
		t.Errorf("deletion didn't work: [%v] [%v]\n", cache, newrecord)
	}
}

func TestClean(t *testing.T) {
	cache := setupCache(t)
	response := setupResponse(t)
	response.Ttl = 10 * time.Second
	response.CreationTime = time.Now().Add(-15 * time.Second)
	cache.Add(response)
	_, ok := cache.Get(response.Key, response.Qtype)
	if !ok {
		t.Errorf("failed to add response: [%v][%v]\n", cache, response)
	}
	cache.Clean()
	newrecord, ok := cache.Get("key", dns.TypeA)
	if ok {
		t.Errorf("cleaning didn't work: [%v] [%v]\n", cache, newrecord)
	}
}
