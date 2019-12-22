package main

type RecordCache struct {
  cache map[string]*Record
}

func (rcache *RecordCache) Add(key string, Record *Record) {
  rcache.cache[key] = Record
}

func (rcache *RecordCache) Get(key string) (*Record, bool){
  val, ok := rcache.cache[key]
  return val, ok
}

func NewCache() (*RecordCache, error) {
  ret := &RecordCache{
    cache: make(map[string]*Record),
  }
  return ret, nil
}
