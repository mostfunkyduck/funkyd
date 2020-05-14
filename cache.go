package main

// TODO: this is now a cache that maps domains to an unsorted collection of records
// this is probably fine because of how small the records for an average domain are
// it could be vastly improved, however, i've tried to make the apis flexible so that i can
// shoehorn in a real system once i proof-of-concept this one
import (
	"fmt"
	"github.com/miekg/dns"
	"time"
)

// constructs a cache key from a response
func formatKey(key string, qtype uint16) string {
	return fmt.Sprintf("%s:%d", key, qtype)
}

func (response Response) IsExpired(rr dns.RR) bool {
	Logger.Log(NewLogMessage(
		DEBUG,
		LogContext{
			"what": fmt.Sprintf("checking if record with ttl [%d] off of creation time [%s] has expired", rr.Header().Ttl, response.CreationTime),
			"next": fmt.Sprintf("returning whether or not the creation time + the TTL is before %s", time.Now()),
		},
		nil,
	))
	return response.CreationTime.Add(time.Duration(rr.Header().Ttl)/time.Second).Before(time.Now())
}

func (r Response) GetExpirationTimeFromRR(rr dns.RR) time.Time {
	return r.CreationTime.Add(time.Duration(rr.Header().Ttl) / time.Second)
}

// Updates the TTL on the cached record so that the client gets the accurate number
func (r Response) updateTtl(rr dns.RR) {
	if r.IsExpired(rr) {
		Logger.Log(NewLogMessage(
			DEBUG,
			LogContext{
				"what": fmt.Sprintf("attempted to update TTL on rr [%v] using response [%v]", rr, r),
			},
			nil,
		))
		return
	}
	expirationTime := r.GetExpirationTimeFromRR(rr)
	ttl := expirationTime.Sub(time.Now()).Seconds()
	castTtl := uint32(ttl)
	Logger.Log(NewLogMessage(
		DEBUG,
		LogContext{
			"what": "updating cached TTL",
		},
		func() string { return fmt.Sprintf("rr [%v] ttl [%f] casted ttl [%d]", rr, ttl, castTtl) },
	))
	rr.Header().Ttl = uint32(ttl)
}

func (r *RecordCache) Size() int {
	return len(r.cache)
}

// can we make it so that this copies the pointers in the response to prevent conflicts
func (r *RecordCache) Add(response Response) {
	r.Lock()
	defer r.Unlock()

	r.cache[formatKey(response.Key, response.Qtype)] = response
	CacheSizeGauge.Set(float64(len(r.cache)))
}

func (r *RecordCache) Get(key string, qtype uint16) (Response, bool) {
	// this class will clean the cache as of now, so it needs a write lock
	r.RLock()
	defer r.RUnlock()
	Logger.Log(NewLogMessage(
		DEBUG,
		LogContext{
			"what": fmt.Sprintf("cache locked, attempting to get [%s] [%d] from cache", key, qtype),
		},
		nil,
	))
	response, ok := r.cache[formatKey(key, qtype)]
	if !ok {
		Logger.Log(NewLogMessage(INFO, LogContext{"what": "cache miss"}, nil))
		return Response{}, false
	}

	// TODO remove this? doesn't seem to be doing anything serious, but who knows?
	if response.Qtype != qtype {
		Logger.Log(NewLogMessage(WARNING, LogContext{"what": "mismatched qtype!", "why": fmt.Sprintf("[%d] != [%d]", response.Qtype, qtype)}, nil))
		return Response{}, false
	}

	Logger.Log(NewLogMessage(
		INFO,
		LogContext{
			"what": "cache hit!",
			"next": "validating and assembling response from rr's",
		},
		func() string { return fmt.Sprintf("%v", response) },
	))
	// there are records for this domain/qtype
	for _, rec := range response.Entry.Answer {
		Logger.Log(NewLogMessage(
			DEBUG,
			LogContext{
				"what": "evaluating validity of record",
				"why":  "assembling response to query",
				"next": "evaluating TTL in cache",
			},
			func() string { return fmt.Sprintf("rec [%v] resp [%v]", rec, response) },
		))
		// just in case the clean job hasn't fired, filter out nastiness NB: the clean job is disabled
		response.updateTtl(rec)
		if response.IsExpired(rec) {
			// There is at least one record in this response that's expired
			// https://tools.ietf.org/html/rfc2181#section-5.2 - if TTLs differ in a RRSET, this is illegal, but you should
			// treat it as if the lowest TTL is the TTL.  A single expiration means that the smallest record is <= its TTL

			// TODO differentiate between synthesized CNAMEs and regular records - CNAMES have long TTLs  since they refer to an A
			// that's holding the actual value, therefore the synthesized A will die before the CNAME itself.
			Logger.Log(NewLogMessage(
				DEBUG,
				LogContext{
					"what": "cached entry has expired",
					"why":  "response contains record with expired TTL",
					"next": "returning cache miss",
				},
				nil,
			))
			r.evict(response)
			return Response{}, false
		}
	}
	Logger.Log(NewLogMessage(DEBUG, LogContext{"what": fmt.Sprintf("returning [%s] from cache get", key)}, nil))
	return response, true
}

// Removes an entire response from the cache
func (r *RecordCache) Remove(response Response) {
	r.Lock()
	defer r.Unlock()
	key := formatKey(response.Key, response.Qtype)
	Logger.Log(NewLogMessage(
		DEBUG,
		LogContext{
			"what": "removing cache entry",
			"key":  key,
		},
		func() string { return fmt.Sprintf("resp [%v] cache [%v]", response, r) },
	))
	delete(r.cache, key)
	CacheSizeGauge.Set(float64(len(r.cache)))
}

func (r *RecordCache) RLock() {
	r.lock.RLock()
}

func (r *RecordCache) RUnlock() {
	r.lock.RUnlock()
}

// can't log before lock, the log function iterates through the map
// which is a nice, delicious race condition with writes
func (r *RecordCache) Lock() {
	r.lock.Lock()
}

func (r *RecordCache) Unlock() {
	r.lock.Unlock()
}

/** cleaning is currently off, we seem to do well enough without it, TBD a real caching algo **/
func (r *RecordCache) Clean() int {
	var records_deleted = 0
	r.Lock()
	defer r.Unlock()

	Logger.Log(NewLogMessage(
		DEBUG,
		LogContext{
			"what": "starting clean job, cache locked",
			"why":  "cleaning record cache",
			"next": "iterating through cache",
		},
		func() string { return fmt.Sprintf("%v", r) },
	))

	// https://tools.ietf.org/html/rfc2181#section-5.2 - if TTLs differ in a RRSET, this is illegal, but you should
	// treat it as if the lowest TTL is the TTL
	for key, response := range r.cache {
		Logger.Log(NewLogMessage(
			DEBUG,
			LogContext{
				"what": fmt.Sprintf("examining entry with key [%s], response [%v]", key, response),
				"why":  "evaluating for cleaning",
				"next": "updating TTLs in all response records and expiring as needed",
			},
			nil,
		))
		for _, record := range response.Entry.Answer {
			// record is valid, update it
			response.updateTtl(record)
			if response.IsExpired(record) {
				// CNAME analysis will have to happen here
				Logger.Log(NewLogMessage(
					INFO,
					LogContext{
						"what": fmt.Sprintf("record [%v] has expired, removing entire cached response [%v]", record, response),
						"why":  "response has expired records",
						"next": "continuing cleaning job on next response",
					},
					func() string { return fmt.Sprintf("%v", r) },
				))
				r.Remove(response)
				records_deleted++
				break
			}
		}
	}
	return records_deleted
}

// spin off a goroutine to contend for the lock and purge the response out of band
func (r *RecordCache) evict(resp Response) {
	// TODO make this into a channel-reading gr instead of allowing for unbounded spawning of eviction grs
	// TODO perhaps combine it with Clean()
	go func(response Response) {
		r.Remove(response)
	}(resp)
}
func NewCache() (*RecordCache, error) {
	ret := &RecordCache{
		cache: make(map[string]Response),
	}
	return ret, nil
}
