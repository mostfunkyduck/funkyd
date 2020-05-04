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
		  "next": fmt.Sprintf("returning whether or not the creation time + the TTL is before %v", time.Now()),
    },
		"",
	))
	return response.CreationTime.Add(time.Duration(rr.Header().Ttl) * time.Second).Before(time.Now())
}

// TODO we could simplify all the expiration logic to have the response iterate through all its records in
// TODO IsExpired and GetExpirationTime instead of the nested for loop happening to check each record currently
// TODO that still needs to be fixed so that expiration is more sensitive and granular
func (r Response) GetExpirationTimeFromRR(rr dns.RR) time.Time {
	return r.CreationTime.Add(time.Duration(rr.Header().Ttl) * time.Second)
}

// Updates the TTL on the cached record so that the client gets the accurate number
func (r Response) updateTtl(rr dns.RR) {
	if r.IsExpired(rr) {
		Logger.Log(NewLogMessage(
			DEBUG,
			LogContext {
        "what": fmt.Sprintf("attempted to update TTL on rr [%v] using response [%v]", rr, r),
      },
			"",
		))
		return
	}
	expirationTime := r.GetExpirationTimeFromRR(rr)
	ttl := expirationTime.Sub(time.Now()).Seconds()
	castTtl := uint32(ttl)
	Logger.Log(NewLogMessage(
		DEBUG,
		LogContext {
      "what": fmt.Sprintf("full ttl of [%v] should be [%f] seconds, cast to uint32, it becomes [%d]", rr, ttl, castTtl),
		  "why": "updating cached TTL",
		  "next": "performing update",
    },
		"",
	))
	rr.Header().Ttl = uint32(ttl)
}

func (r *RecordCache) Size() int {
	return len(r.cache)
}

// can we make it so that this copies the pointers in the response to prevent conflicts
func (rcache *RecordCache) Add(response Response) {
	rcache.Lock()
	defer rcache.Unlock()

	rcache.cache[formatKey(response.Key, response.Qtype)] = response
	CacheSizeGauge.Set(float64(len(rcache.cache)))
}

func (rcache *RecordCache) Get(key string, qtype uint16) (Response, bool) {
	// this class will clean the cache as of now, so it needs a write lock
	rcache.RLock()
	defer rcache.RUnlock()
	Logger.Log(NewLogMessage(
		DEBUG,
    LogContext {
      "what": fmt.Sprintf("cache locked, attempting to get [%s] [%d] from cache", key, qtype),
    },
    "",
	))
	response, ok := rcache.cache[formatKey(key, qtype)]
	if !ok {
		Logger.Log(NewLogMessage(DEBUG, LogContext{"what": "cache miss"}, ""))
		return Response{}, false
	}

	if response.Qtype != qtype {
		Logger.Log(NewLogMessage(WARNING, LogContext{"what": "mismatched qtype!", "why": fmt.Sprintf("[%d] != [%d]", response.Qtype, qtype)}, ""))
		return Response{}, false
	}

	Logger.Log(NewLogMessage(DEBUG, LogContext{"what": "cache hit!", "next": "validating and assembling response from rr's"}, fmt.Sprintf("%v", response)))
	// there are records for this domain
	for _, rec := range response.Entry.Answer {
		Logger.Log(NewLogMessage(
			DEBUG,
      LogContext {
			  "what": fmt.Sprintf("evaluating validity of record [%v]", rec),
			  "why": "assembling response to query",
			  "next": "updating TTL in cache",
      },
			fmt.Sprintf("%v", response)))
		// just in case the clean job hasn't fired, filter out nastiness
		response.updateTtl(rec)
		if response.IsExpired(rec) {
			// https://tools.ietf.org/html/rfc2181#section-5.2 - if TTLs differ in a RRSET, this is illegal, but you should
			// treat it as if the lowest TTL is the TTL.  A single expiration means that the smallest record is <= its TTL

			// TODO differentiate between synthesized CNAMEs and regular records - CNAMES have long TTLs  since they refer to an A
			// that's holding the actual value, therefore the synthesized A will die before the CNAME itself.
			Logger.Log(NewLogMessage(DEBUG, LogContext{ "what": "cached entry has expired", "why": "response contains record with expired TTL", "next": "returning cache miss"}, ""))
			return Response{}, false
		}
	}
	Logger.Log(NewLogMessage(DEBUG, LogContext{ "what": fmt.Sprintf("returning [%v] from cache get", key)}, ""))
	return response, true
}

// Removes an entire response from the cache
func (rcache *RecordCache) Remove(response Response) error {
	key := formatKey(response.Key, response.Qtype)
	Logger.Log(NewLogMessage(
		DEBUG,
		LogContext {
      "what": fmt.Sprintf("removing [%v] from cache using key [%v]", response, key),
		  "next": "deleting from cache",
    },
		"",
	))
	delete(rcache.cache, key)
	CacheSizeGauge.Set(float64(len(rcache.cache)))
	return nil
}

func (rcache *RecordCache) RLock() {
	rcache.lock.RLock()
}

func (rcache *RecordCache) RUnlock() {
	rcache.lock.RUnlock()
}

// can't log before lock, the log function iterates through the map
// which is a nice, delicious race condition with writes
func (rcache *RecordCache) Lock() {
	rcache.lock.Lock()
}

func (rcache *RecordCache) Unlock() {
	rcache.lock.Unlock()
}

func (rcache *RecordCache) Clean() int {
	var records_deleted = 0
	rcache.Lock()
	defer rcache.Unlock()

	Logger.Log(NewLogMessage(
		DEBUG,
		LogContext {
      "what": "starting clean job, cache locked",
		  "why": "cleaning record cache",
		  "next": "iterating through cache",
    },
		fmt.Sprintf("%v", rcache),
	))

	// https://tools.ietf.org/html/rfc2181#section-5.2 - if TTLs differ in a RRSET, this is illegal, but you should
	// treat it as if the lowest TTL is the TTL
	for key, response := range rcache.cache {
		Logger.Log(NewLogMessage(
			DEBUG,
      LogContext {
			  "what": fmt.Sprintf("examining entry with key [%s], response [%v]", key, response),
			  "why": "evaluating for cleaning",
			  "next": "updating TTLs in all response records and expiring as needed",
      },
			"",
		))
		for _, record := range response.Entry.Answer {
			// record is valid, update it
			response.updateTtl(record)
			if response.IsExpired(record) {
				// CNAME analysis will have to happen here
				Logger.Log(NewLogMessage(
					INFO,
          LogContext {
					  "what": fmt.Sprintf("record [%v] has expired, removing entire cached response [%v]", record, response),
					  "why": "response has expired records",
					  "next": "continuing cleaning job on next response",
          },
					fmt.Sprintf("%v", rcache),
				))
				rcache.Remove(response)
				records_deleted++
				break
			}
		}
	}
	return records_deleted
}

// Starts the internal cache clean timer that will periodically prune expired cache entries
func (rcache *RecordCache) Init() {

	Logger.Log(NewLogMessage(INFO, LogContext{ "what": fmt.Sprintf("initializing clean ticket for %d second intervals", 1), "next": "starting ticker"}, ""))
	ticker := time.NewTicker(1 * time.Second)
	go func() {
		for range ticker.C {
			rcache.Clean()
		}
	}()
	return
}

func NewCache() (*RecordCache, error) {
	ret := &RecordCache{
		cache: make(map[string]Response),
	}
	// ret.Init() keep this outside of the constructor for unit testing
	return ret, nil
}
