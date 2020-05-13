package main

import (
	"fmt"
	"github.com/google/go-cmp/cmp"
	"github.com/miekg/dns"
	"testing"
	"time"
)

func setupCache() (rc *RecordCache, err error) {
	return NewCache()
}

func setupResponse(idx int) Response {
	return Response{
		Key:   fmt.Sprintf("%d", idx),
		Entry: dns.Msg{},
	}
}

func TestCache(t *testing.T) {
	_, err := setupCache()
	if err != nil {
		t.Fatalf("couldn't set up cache: %s", err.Error())
	}
}

func TestStorage(t *testing.T) {
	cache, err := setupCache()
	if err != nil {
		t.Fatalf("couldn't set up cache: %s", err.Error())
	}
	response := setupResponse(1)
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
	cache, err := setupCache()
	if err != nil {
		t.Fatalf("couldn't set up cache: %s", err.Error())
	}
	response := setupResponse(1)
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

// queues up a channel with $max responses being fed into it by a separate goroutine
func prepareCacheBenchmark(cache *RecordCache) (c chan Response, expected int) {
	max := 100000
	responses := []Response{}
	// the idea here is to create enough test removals to cause a lot of contention for the lock
	// note that this test will be run with increasingly higher b.N values until it can be timed
	for i := 0; i < max; i++ {
		response := setupResponse(i)
		responses = append(responses, response)
	}
	responseChannel := make(chan Response)
	go func() {
		for _, each := range responses {
			responseChannel <- each
		}
		close(responseChannel)
	}()
	return responseChannel, max
}

func BenchmarkCacheAddParallel(b *testing.B) {
	cache, err := setupCache()
	if err != nil {
		b.Fatalf("couldn't set up cache: %s", err.Error())
	}

	responseChannel, expected := prepareCacheBenchmark(cache)
	b.Log("setup complete, starting tests")
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			for response := range responseChannel {
				cache.Add(response)
			}
		}
	})

	size := cache.Size()
	if size != expected {
		b.Fatalf("cache was not expected size! [%d != %d]", size, expected)
	}
}

func BenchmarkCacheRemoveParallel(b *testing.B) {
	cache, err := setupCache()
	if err != nil {
		b.Fatalf("couldn't set up cache: %s", err.Error())
	}
	responseChannel, expected := prepareCacheBenchmark(cache)
	b.Log("setup complete, starting tests")
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			for response := range responseChannel {
				cache.Remove(response)
			}
		}
	})

	if cache.Size() != 0 {
		b.Fatalf("cache still had entries after removals. cache [%v] expected removals [%d]", cache, expected)
	}
}
