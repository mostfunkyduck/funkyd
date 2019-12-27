package main

// TODO: this is now a cache that maps domains to an unsorted collection of records
// this is probably fine because of how small the records for an average domain are
// it could be vastly improved, however, i've tried to make the apis flexible so that i can
// shoehorn in a real system once i proof-of-concept this one
import (
  "github.com/google/go-cmp/cmp"
  "time"
  "fmt"
  "log"
)

func (rcache *RecordCache) Add(record *Record) {
  log.Printf("adding [%v] to cache\n", record)
  _, ok := rcache.Get(record.Key, record.Qtype)
  if ok {
    // we have records for this query type, 
    // when i overhaul this, we should always be able to blow out all records of a specific kind in order to upsert the cache without worrying about stale or duplicate records being left behind
    // for now... add won't update. 
    // this breaks multiple records for the same domain, only the first will be returned
    return
  }
  // if it's not already in there, let's just shift it in at the end
  rcache.cache[record.Key] = append(rcache.cache[record.Key], record)
}

func (rcache *RecordCache) Get(key string, qtype uint16) ([]*Record, bool){
  var matching_records []*Record = make([]*Record, 0)
  log.Printf("getting [%s] from cache\n", key)
  cached_records, ok := rcache.cache[key]
  if ok {
    log.Printf("retrieved [%v] from cache\n", cached_records)
    // there are records for this domain
    for _, rec := range cached_records {
      log.Printf("evaluating: %v\n", rec)
      // just in case the clean job hasn't fired, filter out nastiness
      if rec.Qtype == qtype && !rec.IsExpired() {
        matching_records = append(matching_records, rec)
      } else {
        log.Printf("%v is expired\n", rec)
      }
    }
    log.Printf("returning [%v]\n", matching_records)
    return matching_records, true
  }
  return cached_records, false
}

func (rcache *RecordCache) Remove(record *Record) error {
  log.Printf("removing [%v] from cache\n", record)
  recs, ok := rcache.cache[record.Key]
  if !ok {
    return fmt.Errorf("could not retrieve record from cache: %v\n", record)
  }

  for i, rec := range recs {
    log.Printf("evaluating [%v] for removal\n", rec)
    if cmp.Equal(record, rec) {
      log.Printf("removing [%v]\n", rec)
      rcache.cache[record.Key] = append(rcache.cache[record.Key][:i], rcache.cache[record.Key][i+1:]...)
    }
    // It's theoretically possible that duplicate records could slip in, so let's prune everything just to be safe
  }

  // if the cache for this domain is now empty, delete the key
  if len(rcache.cache[record.Key]) == 0 {
    delete(rcache.cache, record.Key)
  }
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
  for key, records := range rcache.cache {
    log.Printf("key: [%s], records: [%v]\n", key, records)
    for _, record := range records {
      log.Printf("%v\n", record)
      if record.IsExpired() {
        rcache.Remove(record)
        records_deleted++
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

// Record functions
func (record *Record) IsExpired() bool {
  return record.CreationTime.Add(record.Ttl).Before(time.Now())
}

func NewCache() (*RecordCache, error) {
  ret := &RecordCache{
    cache: make(map[string][]*Record),
  }
  // ret.Init() keep this outside of the constructor for unit testing
  return ret, nil
}
