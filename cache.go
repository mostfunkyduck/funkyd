package main

import (
  "time"
  "fmt"
  "log"
)

func (rcache *RecordCache) Add(record *Record) {
  _, ok := rcache.Get(record.Key, record.Qtype)
  if ok {
    return
  }
  // if it's not already in there, let's just shift it in at the end
  rcache.cache[record.Key] = append(rcache.cache[record.Key], record)
}

func (rcache *RecordCache) Get(key string, qtype uint16) ([]*Record, bool){
  var matching_records []*Record = make([]*Record, 0)
  cached_records, ok := rcache.cache[key]
  if ok {
    // there are records for this domain
    for _, rec := range cached_records {
      log.Printf("in Get: %v\n", rec)
      // just in case the clean job hasn't fired, filter out nastiness
      if rec.Qtype == qtype && !rec.IsExpired() {
        matching_records = append(matching_records, rec)
      }
    }
    return matching_records, true
  }
  return cached_records, false
}

func (rcache *RecordCache) Remove(record *Record) error {
  recs, ok := rcache.cache[record.Key]
  if !ok {
    return fmt.Errorf("could not retrieve record from cache: %v\n", record)
  }

  for i, rec := range recs {
    if rec == record {
      rcache.cache[record.Key] = append(rcache.cache[record.Key][:i], rcache.cache[record.Key][i+1:]...)
    }
  }
  return nil
}

func (rcache *RecordCache) RLock() {
  rcache.lock.RLock()
}

func (rcache *RecordCache) RUnlock() {
  rcache.lock.RUnlock()
}

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

func (rcache *RecordCache) InitCleanTicker() {

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

func (record *Record) IsExpired() bool {
  return record.CreationTime.Add(record.Ttl).Before(time.Now())
}

func NewCache() (*RecordCache, error) {
  ret := &RecordCache{
    cache: make(map[string][]*Record),
  }
  ret.InitCleanTicker()
  return ret, nil
}
