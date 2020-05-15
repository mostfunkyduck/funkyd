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

// Cleans the cache periodically, evicting all bad responses for the trashman
type Janitor interface {
	// Starts the janitor
	Start(r *RecordCache)

	// Stops the janitor
	Stop ()
}

type janitor struct {
	Cancel chan bool
}

// The trashman is in charge of actually removing responses
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
	Cancel	chan bool

	// channel for taking evicted responses
	Channel chan Response

	// cache that the trashman is collecting trash for
	recordCache *RecordCache

	// responses buffered to be discarded
	responses map[string]Response

	// how many responses to buffer before flushing
	evictionBatchSize int

	// channel for telling the trashman to pause or unpause
	pauseChannel	chan bool

	// whether or not this trashman is paused
	paused	bool
}

// Core cache struct, manages the actual cache and the cleaning crew
type RecordCache struct {
	// See janitor struct
	Janitor		 Janitor

	// See TrashMan struct
	TrashMan	 TrashMan

	// the actual cache
	cache      map[string]Response

	// the cache lock
	lock       Lock
}

// DNS response cache wrapper
type Response struct {
	// The domain this response is for
	Key          string

	// The actual reply message
	Entry        dns.Msg

	// TTL
	Ttl          time.Duration

	// Query type
	Qtype        uint16

	// When this response was created
	CreationTime time.Time
}

// constructs a cache key from a response
func (r Response) FormatKey() string {
	return r.Key + string(r.Qtype)
}

func (response Response) IsExpired(rr dns.RR) bool {
	expired := response.CreationTime.Add(time.Duration(rr.Header().Ttl) * time.Second).Before(time.Now())
	Logger.Log(NewLogMessage(
		DEBUG,
		LogContext{
			"what": "checking if record has expired",
			"ttl": fmt.Sprintf("%d", rr.Header().Ttl),
			"record_key": response.FormatKey(),
			"creationtime": fmt.Sprintf("%s", response.CreationTime),
			"expired": fmt.Sprintf("%t", expired),
		},
		nil,
	))
	return expired
}

func (r Response) GetExpirationTimeFromRR(rr dns.RR) time.Time {
	return r.CreationTime.Add(time.Duration(rr.Header().Ttl) * time.Second)
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
			"ttl": string(castTtl),
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
	r.cache[response.FormatKey()] = response
	CacheSizeGauge.Set(float64(len(r.cache)))
}

func (r *RecordCache) Get(key string, qtype uint16) (Response, bool) {
	// this class will clean the cache as of now, so it needs a write lock
	r.RLock()
	defer r.RUnlock()
	Logger.Log(NewLogMessage(
		DEBUG,
		LogContext{
			"what": Logger.Sprintf(DEBUG, "cache locked, attempting to get [%s] [%d] from cache", key, qtype),
		},
		nil,
	))

	response := Response{
		Key: key,
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
			r.Evict(response)
			return Response{}, false
		}
	}
	Logger.Log(NewLogMessage(DEBUG, LogContext{"what": Logger.Sprintf(DEBUG, "returning [%s] from cache get", key)}, nil))
	return response, true
}

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

func (r *RecordCache) RemoveSlice (responses []Response) {
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
				"what": "examining entry",
				"key":  key,
				"why":  "evaluating for cleaning",
				"next": "updating TTLs in all response records and expiring as needed",
			},
			func () string { return fmt.Sprintf("resp: [%v]", response)},
		))
		for _, record := range response.Entry.Answer {
			if response.IsExpired(record) {
				// CNAME analysis will have to happen here
				Logger.Log(NewLogMessage(
					DEBUG,
					LogContext {
						"what": "evicting response",
						"key": response.FormatKey(),
					},
					nil,
				))
				r.Evict(response)
				records_deleted++
				break
			}
		}
	}
	return records_deleted
}

// tell the trashman to dispose of this response
func (r *RecordCache) Evict(resp Response) {
	// need to async or there will be deadlock when a caller is holding
	// r's lock and this function has to essentially trigger a 'Remove' 
	// that needs that lock to progress
	go func() {
		r.TrashMan.Evict(resp)
	}()
}

func NewCache() (*RecordCache, error) {
	ret := &RecordCache{
		cache: make(map[string]Response),
	}
	return ret, nil
}

func (r *RecordCache) StartCleaningCrew() {
	if r.Janitor == nil {
		r.Janitor = &janitor{}
	}

	if r.TrashMan == nil {
		r.TrashMan = &trashMan{
			responses: make(map[string]Response),
			evictionBatchSize: GetConfiguration().EvictionBatchSize,
		}
	}

	r.Janitor.Start(r)
	r.TrashMan.Start(r)
}

// Mainly allowing this so that tests can clean up their grs
func (r *RecordCache) StopCleaningCrew() {
	r.TrashMan.Stop()
	r.Janitor.Stop()
}

func (t *trashMan) Stop() {
	t.Cancel <-true
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
		LogContext {
			"what": "trashman flushing evicted records",
			"recordcount": string(t.ResponsesQueued()),
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
	t.pauseChannel <-true
}

func (t *trashMan) Unpause() {
	t.pauseChannel <-false
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
			LogContext {
				"what": "starting trashman",
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
					LogContext {
						"what": "trashman discarding response",
						"response": response.FormatKey(),
						"queued_responses": fmt.Sprintf("%d", t.ResponsesQueued()),
					},
					func () string { return fmt.Sprintf("response: [%v]", response)},
				))
				t.AddResponse(response)
				if t.ResponsesQueued() >= t.evictionBatchSize && ! t.paused {
					t.FlushResponses()
				}
				EvictionBufferGauge.Set(float64(t.ResponsesQueued()))
			case p := <-t.pauseChannel:
				t.paused = p
				// if we just unpaused, flush!
				if ! p && t.ResponsesQueued() >= t.evictionBatchSize {
					t.FlushResponses()
				}
			case <-t.Cancel:
				break tmloop
			}
		}
	}()
}

func (j janitor) Stop() {
	j.Cancel <-true
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
			LogContext {
				"what": "starting janitor",
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

