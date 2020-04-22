package main

// TODO: this is now a cache that maps domains to an unsorted collection of records
// this is probably fine because of how small the records for an average domain are
// it could be vastly improved, however, i've tried to make the apis flexible so that i can
// shoehorn in a real system once i proof-of-concept this one
import (
  "github.com/miekg/dns"
  "log"
  "time"
)

func (response Response) IsExpired(rr dns.RR) bool {
  log.Printf("checking if record with ttl [%d] off of creation time [%s]  has expired", rr.Header().Ttl, response.CreationTime)
  return response.CreationTime.Add(time.Duration(rr.Header().Ttl) * time.Second).Before(time.Now())
}

func (response Response) updateTtl(rr dns.RR) {
  // https://stackoverflow.com/questions/26285735/subtracting-time-duration-from-time-in-go oh wtf
  expirationTime := response.CreationTime.Add(time.Duration(rr.Header().Ttl) * time.Second)
  if expirationTime.Before(time.Now()) {
    log.Printf("attempted to update expired ttl for record [%v]\n", rr)
    return
  }

  ttl := expirationTime.Sub(time.Now()).Seconds()
  log.Printf("setting ttl on [%v] to be [%d] seconds [%d] as a uint32\n", rr, ttl, uint32(ttl))
  rr.Header().Ttl = uint32(ttl)

}

// can we make it so that this copies the pointers in the response to prevent conflicts
func (rcache *RecordCache) Add(response Response) {
  log.Printf("adding [%s] to cache\n", response.Key)
  _, ok := rcache.Get(response.Key, response.Qtype)
  if ok {
    // we have records for this query type,
    // when i overhaul this, we should always be able to blow out all records of a specific kind in order to upsert the cache without worrying about stale or duplicate records being left behind
    // for now... add won't update.
    // this breaks multiple records for the same domain, only the first will be returned
    return
  }
  // if it's not already in there, let's just shift it in at the end
  rcache.cache[response.Key] = response
  CacheSizeGauge.Inc()
}

func (rcache *RecordCache) Get(key string, qtype uint16) (Response, bool) {
  var RRs []dns.RR
  log.Printf("getting [%s] from cache\n", key)
  response, ok := rcache.cache[key]
  if !ok {
    log.Printf("cache miss")
    return Response{}, false
  }

  if response.Qtype != qtype {
    log.Printf("mismatched qtype! [%d] != [%d]", response.Qtype, qtype)
    return Response{}, false
  }

  log.Printf("retrieved [%v] from cache\n", response)
  // there are records for this domain
  for _, rec := range response.Entry.Answer {
    log.Printf("evaluating: %v\n", rec)
    // just in case the clean job hasn't fired, filter out nastiness
    if !response.IsExpired(rec) {
      response.updateTtl(rec)
      RRs = append(RRs, rec)
    } else {
      // https://tools.ietf.org/html/rfc2181#section-5.2 - if TTLs differ in a RRSET, this is illegal, but you should
      // treat it as if the lowest TTL is the TTL.  A single expiration means that the smallest record is <= its TTL

      // TODO differentiate between synthesized CNAMEs and regular records - CNAMES have long TTLs  since they refer to an A
      // that's holding the actual value, therefore the synthesized A will die before the CNAME itself.
      return Response{}, false
    }
  }
  log.Printf("returning [%v]\n", RRs)
  response.Entry.Answer = RRs
  return response, true
}

// Removes an entire response from the cache
func (rcache *RecordCache) Remove(response Response) error {
  log.Printf("removing [%v] from cache\n", response)
  CacheSizeGauge.Dec()
  delete(rcache.cache, response.Key)
  return nil
}

func (rcache *RecordCache) RLock() {
  rcache.lock.RLock()
  log.Printf("RLocking [%v]\n", rcache)
}

func (rcache *RecordCache) RUnlock() {
  log.Printf("RUnlocking [%v]\n", rcache)
  rcache.lock.RUnlock()
}

// can't log before lock, the log function iterates through the map
// which is a nice, delicious race condition with writes
func (rcache *RecordCache) Lock() {
  rcache.lock.Lock()
  log.Printf("Locking [%v]\n", rcache)
}

func (rcache *RecordCache) Unlock() {
  log.Printf("Unlocking [%v]\n", rcache)
  rcache.lock.Unlock()
}

func (rcache *RecordCache) Clean() int {
  var records_deleted = 0
  rcache.Lock()
  defer rcache.Unlock()

  // https://tools.ietf.org/html/rfc2181#section-5.2 - if TTLs differ in a RRSET, this is illegal, but you should
  // treat it as if the lowest TTL is the TTL
  for key, response := range rcache.cache {
    log.Printf("key: [%s], response: [%v]\n", key, response)
    for _, record := range response.Entry.Answer {
      log.Printf("evaluating [%v] for expiration\n", response)
      if response.IsExpired(record) {
        // CNAME analysis will have to happen here
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

  log.Printf("preparing clean time to launch after 5 seconds")
  ticker := time.NewTicker(5 * time.Second)
  go func() {
    for range ticker.C {
      log.Printf("starting clean operation\n")
      recs_deleted := rcache.Clean()
      log.Printf("deleted %d records during clean timer execution\n", recs_deleted)
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
