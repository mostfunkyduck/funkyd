package main

// TODO: this is now a cache that maps domains to an unsorted collection of records
// TODO this is probably fine because of how small the records for an average domain are
// TODO it could be vastly improved, however, i've tried to make the apis flexible so that i can
// TODO shoehorn in a real system once i proof-of-concept this one
import (
	"fmt"
	"github.com/miekg/dns"
	"time"
	"sync"
)

// Cache models a basic cache
type Cache interface {
	// Add to the cache
	Add(response Response)

	// Retrieve a response by name and query type
	Get(name string, qtype uint16) (r Response, ok bool)

	// get current cache size
	Size() int

	// remove a response from the cache
	Remove(response Response)

	// remove a slice of responses from the cache
	RemoveSlice(responses []Response)

	// generic lock and unlock functions
	RLock()

	RUnlock()

	Lock()

	Unlock()

	// removes stale entries from the cache
	Clean() int

	// starts the goroutines that handle cache cleaning and eviction
	StartCleaningCrew()

	// stops said cleaning crew
	StopCleaningCrew()

	// queues a response to be removed from the cache
	Evict(resp Response)
}

// Janitor cleans a cache periodically, evicting all bad responses for the trashman
type Janitor interface {
	// Starts the janitor
	Start(r *RecordCache)

	// Stops the janitor
	Stop()
}

type janitor struct {
	Cancel chan bool
}

// TrashMan - The trashman is in charge of actually removing responses
// from a given cache.  As removal requires a lock, the trashman
// interface provides functions for queueing and flushing responses
// to avoid thrashing
type TrashMan interface {
	// Initializes the trashman for a given cache
	Start(r *RecordCache)

	// Halts the trashman
	Stop()

	// queues a response for discarding
	Evict(r Response)

	// returns how many responses are queued for discarding
	ResponsesQueued() int

	// queues a response to be discarded
	AddResponse(r Response)

	// deletes all queued records
	FlushResponses()

	// pauses the trashman (used to avoid timing crap in unit tests, while still being able to test end to end)
	Pause()

	// Unpauses
	Unpause()

	// returns paused status
	Paused() bool
}

// Waits for evicted responses and deletes them out of band
type trashMan struct {
	// cancel channel for teardown
	Cancel chan bool

	// channel for taking evicted responses
	Channel chan Response

	// cache that the trashman is collecting trash for
	recordCache *RecordCache

	// responses buffered to be discarded
	responses map[string]Response

	// how many responses to buffer before flushing
	evictionBatchSize int

	// channel for telling the trashman to pause or unpause
	pauseChannel chan bool

	// whether or not this trashman is paused
	paused bool
}

// RecordCache - Core cache struct, manages the actual cache and the cleaning crew
type RecordCache struct {
	// See janitor struct
	Janitor Janitor

	// See TrashMan struct
	TrashMan TrashMan

	// the actual cache
	cache map[string]Response

	// the cache lock
	lock sync.RWMutex
}

// Response DNS response cache wrapper
type Response struct {
	// The domain this response is for
	Name string

	// The actual reply message
	Entry dns.Msg

	// TTL
	TTL time.Duration

	// Query type
	Qtype uint16

	// When this response was created
	CreationTime time.Time
}

// FormatKey - constructs a cache key
func (r *Response) FormatKey() string {
	return fmt.Sprintf("%s:%d", r.Name, r.Qtype)
}

// IsExpired - tests whether a response is expired
func (r *Response) IsExpired(rr dns.RR) bool {
	expired := r.CreationTime.Add(time.Duration(rr.Header().Ttl) * time.Second).Before(time.Now())
	Logger.Log(NewLogMessage(
		DEBUG,
		LogContext{
			"what":         "checking if record has expired",
			"ttl":          fmt.Sprintf("%d", rr.Header().Ttl),
			"record_key":   r.FormatKey(),
			"creationtime": fmt.Sprintf("%s", r.CreationTime),
			"expired":      fmt.Sprintf("%t", expired),
		},
		nil,
	))
	return expired
}


// Updates the TTL on the cached record so that the client gets the accurate number
func (r *Response) updateTTL(rr dns.RR) {
	if r.IsExpired(rr) {
		Logger.Log(NewLogMessage(
			DEBUG,
			LogContext{
				"what": Logger.Sprintf(DEBUG, "attempted to update TTL on rr [%v] using response [%v]", rr, r),
			},
			nil,
		))
		return
	}
	expirationTime := r.CreationTime.Add(time.Duration(rr.Header().Ttl) * time.Second)
	ttl := expirationTime.Sub(time.Now()).Seconds()
	castTTL := uint32(ttl)
	Logger.Log(NewLogMessage(
		DEBUG,
		LogContext{
			"what": "updating cached TTL",
			"ttl":  string(castTTL),
		},
		func() string { return fmt.Sprintf("rr [%v] ttl [%f] casted ttl [%d]", rr, ttl, castTTL) },
	))
	rr.Header().Ttl = uint32(ttl)
}

// Size returns the size of the cache
func (r *RecordCache) Size() int {
	return len(r.cache)
}

// Add adds a response to the cache
// can we make it so that this copies the pointers in the response to prevent conflicts
func (r *RecordCache) Add(response Response) {
	r.Lock()
	defer r.Unlock()
	r.cache[response.FormatKey()] = response
	CacheSizeGauge.Set(float64(len(r.cache)))
}

// Get returns a response from the cache given a name and query type
func (r *RecordCache) Get(name string, qtype uint16) (Response, bool) {
	// this class will clean the cache as of now, so it needs a write lock
	r.RLock()
	defer r.RUnlock()
	Logger.Log(NewLogMessage(
		DEBUG,
		LogContext{
			"what": Logger.Sprintf(DEBUG, "cache locked, attempting to get [%s] [%d] from cache", name, qtype),
		},
		nil,
	))

	response := Response{
		Name:  name,
		Qtype: qtype,
	}
	response, ok := r.cache[response.FormatKey()]
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
		// make sure the TTL is up to date
		response.updateTTL(rec)
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
			r.Evict(response)
			return Response{}, false
		}
	}
	Logger.Log(NewLogMessage(DEBUG, LogContext{"what": Logger.Sprintf(DEBUG, "returning [%s] from cache get", name)}, nil))
	return response, true
}

// Remove removes a response from the cache
func (r *RecordCache) Remove(response Response) {
	r.Lock()
	defer r.Unlock()
	r.remove(response)
}

// Removes an entire response from the cache, helper function, not reentrant
func (r *RecordCache) remove(response Response) {
	key := response.FormatKey()
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

// RemoveSlice removes a slice of responses from the cache
// useful for doing bulk deletion while only locking the cache once
func (r *RecordCache) RemoveSlice(responses []Response) {
	r.Lock()
	defer r.Unlock()
	for _, resp := range responses {
		r.remove(resp)
	}
}

// RLock read-lock the cache
func (r *RecordCache) RLock() {
	r.lock.RLock()
}

// RUnlock read-unlock the cache
func (r *RecordCache) RUnlock() {
	r.lock.RUnlock()
}

// Lock locks the cache
func (r *RecordCache) Lock() {
	// can't log before lock, the log function iterates through the map
	// which is a nice, delicious race condition with writes
	r.lock.Lock()
}

// Unlock unlocks the cache
func (r *RecordCache) Unlock() {
	r.lock.Unlock()
}

// Clean iterates through the cache and evicts expired responses
func (r *RecordCache) Clean() int {
	var recordsDeleted = 0
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
				"what": "examining entry",
				"key":  key,
				"why":  "evaluating for cleaning",
				"next": "updating TTLs in all response records and expiring as needed",
			},
			func() string { return fmt.Sprintf("resp: [%v]", response) },
		))
		for _, record := range response.Entry.Answer {
			if response.IsExpired(record) {
				// CNAME analysis will have to happen here
				Logger.Log(NewLogMessage(
					DEBUG,
					LogContext{
						"what": "evicting response",
						"key":  response.FormatKey(),
					},
					nil,
				))
				r.Evict(response)
				recordsDeleted++
				break
			}
		}
	}
	return recordsDeleted
}

// Evict asynchronously removes a response from the cache
func (r *RecordCache) Evict(resp Response) {
	// need to async or there will be deadlock when a caller is holding
	// r's lock and this function has to essentially trigger a 'Remove'
	// that needs that lock to progress
	go func() {
		r.TrashMan.Evict(resp)
	}()
}

// NewCache initializes the caches
func NewCache() (Cache, error) {
	ret := &RecordCache{
		cache: make(map[string]Response),
	}
	return ret, nil
}

// StartCleaningCrew initializes a Janitor and Trashman to keep the cache clean
func (r *RecordCache) StartCleaningCrew() {
	if r.Janitor == nil {
		r.Janitor = &janitor{}
	}

	if r.TrashMan == nil {
		r.TrashMan = &trashMan{
			responses:         make(map[string]Response),
			evictionBatchSize: GetConfiguration().EvictionBatchSize,
		}
	}

	r.Janitor.Start(r)
	r.TrashMan.Start(r)
}

// StopCleaningCrew deactivates the cleaning crew
// Mainly allowing this so that tests can clean up their grs
func (r *RecordCache) StopCleaningCrew() {
	r.TrashMan.Stop()
	r.Janitor.Stop()
}

func (t *trashMan) Stop() {
	t.Cancel <- true
}
func (t *trashMan) Evict(r Response) {
	t.Channel <- r
}
func (t *trashMan) AddResponse(r Response) {
	if _, ok := t.responses[r.FormatKey()]; !ok {
		t.responses[r.FormatKey()] = r
	}
}

func (t *trashMan) FlushResponses() {
	Logger.Log(NewLogMessage(
		INFO,
		LogContext{
			"what":        "trashman flushing evicted records",
			"recordcount": fmt.Sprintf("%d", t.ResponsesQueued()),
		},
		nil,
	))

	flushBuffer := []Response{}
	for k, v := range t.responses {
		flushBuffer = append(flushBuffer, v)
		delete(t.responses, k)
	}
	t.recordCache.RemoveSlice(flushBuffer)
}

func (t *trashMan) Pause() {
	t.pauseChannel <- true
}

func (t *trashMan) Unpause() {
	t.pauseChannel <- false
}

func (t *trashMan) ResponsesQueued() int {
	return len(t.responses)
}

func (t *trashMan) Paused() bool {
	return t.paused
}
func (t *trashMan) Start(r *RecordCache) {
	t.Channel = make(chan Response)
	t.Cancel = make(chan bool)
	t.pauseChannel = make(chan bool)
	t.recordCache = r
	go func() {
		Logger.Log(NewLogMessage(
			INFO,
			LogContext{
				"what":      "starting trashman",
				"batchsize": string(t.evictionBatchSize),
			},
			nil,
		))
	tmloop:
		for {
			select {
			case response := <-t.Channel:
				Logger.Log(NewLogMessage(
					DEBUG,
					LogContext{
						"what":             "trashman discarding response",
						"response":         response.FormatKey(),
						"queued_responses": fmt.Sprintf("%d", t.ResponsesQueued()),
					},
					func() string { return fmt.Sprintf("response: [%v]", response) },
				))
				t.AddResponse(response)
				if t.ResponsesQueued() >= t.evictionBatchSize && !t.paused {
					t.FlushResponses()
				}
				EvictionBufferGauge.Set(float64(t.ResponsesQueued()))
			case p := <-t.pauseChannel:
				t.paused = p
				// if we just unpaused, flush!
				if !p && t.ResponsesQueued() >= t.evictionBatchSize {
					t.FlushResponses()
				}
			case <-t.Cancel:
				break tmloop
			}
		}
	}()
}

func (j janitor) Stop() {
	j.Cancel <- true
}

func (j janitor) Start(r *RecordCache) {
	config := GetConfiguration()
	j.Cancel = make(chan bool)
	go func() {
		interval := config.CleanInterval
		if interval == 0 {
			interval = 1000
		}
		t := time.NewTicker(time.Duration(interval) * time.Millisecond)
		Logger.Log(NewLogMessage(
			INFO,
			LogContext{
				"what":     "starting janitor",
				"interval": fmt.Sprintf("%d", interval),
			},
			nil,
		))
		for {
			select {
			case _ = <-t.C:
				r.Clean()
			case _ = <-j.Cancel:
				return
			}
		}
	}()
}
