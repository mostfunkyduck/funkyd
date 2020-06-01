package main

// Cache module for storing RRs.
import (
	"fmt"
	"time"

	"github.com/miekg/dns"
)

type Cache interface {
	// Add to the cache
	Add(response Response)

	// Retrieve a response by name and query type
	Get(name string, qtype uint16) (r Response, ok bool)

	// get current cache size
	Size() int

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
}

// Cleans the cache periodically, evicting all bad responses for the trashman.
type Janitor interface {
	// Starts the janitor
	Start(r *RecordCache)

	// Stops the janitor
	Stop()
}

type janitor struct {
	Cancel chan bool
}

// The trashman is in charge of actually removing responses
// from a given cache.  As removal requires a lock, the trashman
// interface provides functions for queueing and flushing responses
// to avoid thrashing.
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

// Waits for evicted responses and deletes them out of band.
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

// Core cache struct, manages the actual cache and the cleaning crew.
type RecordCache struct {
	// See janitor struct
	Janitor Janitor

	// See TrashMan struct
	TrashMan TrashMan

	// the actual cache
	cache map[string]Response

	// the cache lock
	lock Lock
}

// DNS response cache wrapper.
type Response struct {
	// The domain this response is for
	Name string

	// The actual reply message
	Entry dns.Msg

	// TTL
	// nolint:stylecheck // miekg/dns uses 'Ttl', might as well be consistent
	Ttl time.Duration

	// Query type
	Qtype uint16

	// When this response was created
	CreationTime time.Time
}

// constructs a cache key from a response.
func (r Response) FormatKey() string {
	return fmt.Sprintf("%s:%d", r.Name, r.Qtype)
}

// Determines if the given rr is expired based on the metadata stored in the response.
func (r Response) RecordExpired(rr dns.RR) bool {
	expired := r.CreationTime.Add(time.Duration(rr.Header().Ttl) * time.Second).Before(time.Now())
	return expired
}

// Returns the TTL for this entry based on when it was initially created.
func (r Response) getTTL() (ttl uint32) {
	expirationTime := r.CreationTime.Add(r.Ttl)
	ttl = uint32(time.Until(expirationTime).Seconds())
	return
}

// Retrieves cache size.
func (r *RecordCache) Size() int {
	return len(r.cache)
}

// Adds to the cache.
func (r *RecordCache) Add(response Response) {
	r.Lock()
	defer r.Unlock()
	r.cache[response.FormatKey()] = response
	CacheSizeGauge.Set(float64(len(r.cache)))
}

// Retrieve a cached response by name and qtype.
func (r *RecordCache) Get(name string, qtype uint16) (Response, bool) {
	// this class will clean the cache as of now, so it needs a write lock
	r.RLock()
	defer r.RUnlock()
	Logger.Log(LogMessage{
		Level: DEBUG,
		Context: LogContext{
			"what": Logger.Sprintf(DEBUG, "cache locked, attempting to get [%s] [%d] from cache", name, qtype),
		},
	})

	response := Response{
		Name:  name,
		Qtype: qtype,
	}
	response, ok := r.cache[response.FormatKey()]

	if !ok {
		Logger.Log(LogMessage{Level: INFO, Context: LogContext{"what": "cache miss"}})
		return Response{}, false
	}

	Logger.Log(LogMessage{
		Level: INFO,
		Context: LogContext{
			"what":     "cache hit!",
			"response": Logger.Sprintf(INFO, "%v", response),
		},
	})
	// there are records for this domain/qtype
	for _, rec := range response.Entry.Answer {
		rec.Header().Ttl = response.getTTL()
		if response.RecordExpired(rec) {
			// There is at least one record in this response that's expired
			// https://tools.ietf.org/html/rfc2181#section-5.2 - if TTLs differ in a RRSET, this is illegal, but you should
			// treat it as if the lowest TTL is the TTL.  A single expiration means that the smallest record is <= its TTL

			// TODO differentiate between synthesized CNAMEs and regular records -
			// CNAMES have long TTLs  since they refer to an A that's holding the actual value,
			// therefore the synthesized A will die before the CNAME itself.
			Logger.Log(LogMessage{
				Level: DEBUG,
				Context: LogContext{
					"what": "cached entry has expired",
					"why":  "response contains record with expired TTL",
					"next": "returning cache miss",
				},
			})
			go r.TrashMan.Evict(response)
			return Response{}, false
		}
	}
	Logger.Log(LogMessage{
		Level: DEBUG,
		Context: LogContext{
			"what": "returning from cache get",
			"item": name,
		},
	})
	return response, true
}

// Removes an entire response from the cache, helper function, not reentrant.
func (r *RecordCache) remove(response Response) {
	key := response.FormatKey()
	Logger.Log(LogMessage{
		Level: DEBUG,
		Context: LogContext{
			"what":     "removing cache entry",
			"key":      key,
			"response": Logger.Sprintf(DEBUG, "%v", response),
			"cache":    Logger.Sprintf(DEBUG, "%v", r),
		},
	})
	delete(r.cache, key)
	CacheSizeGauge.Set(float64(len(r.cache)))
}

func (r *RecordCache) RemoveSlice(responses []Response) {
	r.Lock()
	defer r.Unlock()
	for _, resp := range responses {
		r.remove(resp)
	}
}

func (r *RecordCache) RLock() {
	r.lock.RLock()
}

func (r *RecordCache) RUnlock() {
	r.lock.RUnlock()
}

func (r *RecordCache) Lock() {
	r.lock.Lock()
}

func (r *RecordCache) Unlock() {
	r.lock.Unlock()
}

func (r *RecordCache) Clean() int {
	var recordsDeleted = 0
	r.Lock()
	defer r.Unlock()

	Logger.Log(LogMessage{
		Level: DEBUG,
		Context: LogContext{
			"what": "starting clean job, cache locked",
			"why":  "cleaning record cache",
		},
	})

	// https://tools.ietf.org/html/rfc2181#section-5.2 - if TTLs differ in a RRSET, this is illegal, but you should
	// treat it as if the lowest TTL is the TTL
	for key, response := range r.cache {
		Logger.Log(LogMessage{
			Level: DEBUG,
			Context: LogContext{
				"what":     "examining entry",
				"key":      key,
				"why":      "evaluating for cleaning",
				"next":     "updating TTLs in all response records and expiring as needed",
				"response": Logger.Sprintf(DEBUG, "%v", response),
			},
		})
		for _, record := range response.Entry.Answer {
			if response.RecordExpired(record) {
				// TODO CNAME analysis will have to happen here
				Logger.Log(LogMessage{
					Level: DEBUG,
					Context: LogContext{
						"what": "evicting response",
						"key":  response.FormatKey(),
					},
				})
				go r.TrashMan.Evict(response)
				recordsDeleted++
				break
			}
		}
	}
	return recordsDeleted
}

func NewCache() Cache {
	ret := &RecordCache{
		cache: make(map[string]Response),
	}
	return ret
}

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

// Mainly allowing this so that tests can clean up their grs.
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
	Logger.Log(LogMessage{
		Level: INFO,
		Context: LogContext{
			"what":        "trashman flushing evicted records",
			"recordcount": fmt.Sprintf("%d", t.ResponsesQueued()),
		},
	})

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
		Logger.Log(LogMessage{
			Level: INFO,
			Context: LogContext{
				"what":      "starting trashman",
				"batchsize": fmt.Sprintf("%d", t.evictionBatchSize),
			},
		})
		for {
			select {
			case response := <-t.Channel:
				Logger.Log(LogMessage{
					Level: DEBUG,
					Context: LogContext{
						"what":             "trashman discarding response",
						"response":         response.FormatKey(),
						"queued_responses": fmt.Sprintf("%d", t.ResponsesQueued()),
						"full_response":    Logger.Sprintf(DEBUG, "%v", response),
					},
				})
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
				return
			}
		}
	}()
}

func (j *janitor) Stop() {
	j.Cancel <- true
}

func (j *janitor) Start(r *RecordCache) {
	config := GetConfiguration()
	j.Cancel = make(chan bool)
	go func() {
		interval := config.CleanInterval
		if interval == 0 {
			interval = 1000
		}
		t := time.NewTicker(interval * time.Millisecond)
		Logger.Log(LogMessage{
			Level: INFO,
			Context: LogContext{
				"what":     "starting janitor",
				"interval": fmt.Sprintf("%d", interval),
			},
		})
		for {
			select {
			case <-t.C:
				r.Clean()
			case <-j.Cancel:
				return
			}
		}
	}()
}
