package main

import (
	"fmt"
	"github.com/google/go-cmp/cmp"
	"github.com/miekg/dns"
	"testing"
	"time"
)

func setupCache() (rc *RecordCache, err error) {
	rc, err = NewCache()
	if err != nil {
		return nil, err
	}
	// the janitor will get in the way of unit testing as it automatically
	// purges recoords, better to leave it stubbed
	rc.Janitor = &StubJanitor{}

	// the trashman will be normal by default
	return
}

func setupResponse(idx int) Response {
	return Response{
		Name:  fmt.Sprintf("%d", idx),
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
	cachedResponse, ok := cache.Get(response.Name, response.Qtype)
	if !ok {
		t.Errorf("cache retrieval failed")
		return
	}

	if !cmp.Equal(cachedResponse, response) {
		t.Errorf("cache retrieval didn't return record matching what was inserted [%v][%v]\n", cachedResponse, response)
	}

	cache.Remove(response)
	newrecord, ok := cache.Get(response.Name, response.Qtype)
	if ok {
		t.Errorf("deletion didn't work: [%v] [%v]\n", cache, newrecord)
	}
}

func TestClean(t *testing.T) {
	cache, err := setupCache()
	if err != nil {
		t.Fatalf("couldn't set up cache: %s", err.Error())
	}
	cache.TrashMan = &trashMan{
		responses:         make(map[string]Response),
		evictionBatchSize: 2,
	}
	// we want the trashman to run here, the janitor is stubbed by the setup function above
	cache.StartCleaningCrew()
	defer cache.StopCleaningCrew()

	i := 1
	f := func(domain string) Response {
		r := setupResponse(i)
		i++
		r.Ttl = 10 * time.Second
		r.CreationTime = time.Now().Add(-15 * time.Second)
		rr, err := dns.NewRR(fmt.Sprintf("%s\t10\tIN\tA\t10.0.0.0", domain))
		if err != nil {
			t.Fatalf("could not create test record: %s", err.Error())
		}
		r.Entry = dns.Msg{
			Answer: []dns.RR{
				rr,
			},
		}
		return r
	}

	// first, test that 'clean' respects the trashman's eviction queue size
	response := f("example.com")
	// NOTE: this assumes the cache will add expired records
	cache.Add(response)

	cache.TrashMan.Pause()
	if !WaitForCondition(10, func() bool {
		return cache.TrashMan.Paused()
	}) {
		t.Fatalf("trashman never paused: [%v]", cache.TrashMan)
	}
	cache.Clean()
	WaitForCondition(10, func() bool {
		return cache.TrashMan.ResponsesQueued() > 0
	})

	// NOTE the 'Get' func can't be called here because the record we added is expired
	// NOTE and it short circuits if it gets an expired record

	if bufferSize := cache.TrashMan.ResponsesQueued(); bufferSize != 1 {
		t.Fatalf("trashman isn't queueing responses: %d != 1", bufferSize)
	}

	// now test the triggered flush behavior
	response2 := f("example2.com")
	cache.Add(response2)
	cache.Clean()
	if !WaitForCondition(10, func() bool {
		return cache.TrashMan.ResponsesQueued() == 2
	}) {
		t.Fatalf("trashman didn't queue second response! resp [%v] cache [%v] tm [%v] rq[%d]", response2, cache, cache.TrashMan, cache.TrashMan.ResponsesQueued())
	}

	cache.TrashMan.Unpause()
	// I hate doing this, but it's the only way to do an end to end test
	if !WaitForCondition(10, func() bool {
		return cache.Size() == 0
	}) {
		t.Fatalf("trash man did not actually delete the records! %d != 0 (after test closure ran), cache: [%v] trashman [%v]", cache.Size(), cache, cache.TrashMan)
	}

	if bufferSize := cache.TrashMan.ResponsesQueued(); bufferSize != 0 {
		t.Fatalf("trash man did not queue records evicted by the clean function! %d != 0", bufferSize)
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
