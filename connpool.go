package main

// Pools connections to upstream servers, does high level lifecycle management and
// prioritizes which upstreams get connections and which don't
import (
	"fmt"
	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
	"sort"
	"sync"
	"time"
)

type DialFunc func(address string) (*dns.Conn, error)

type ConnEntry interface {
	// Add an exchange with a duration to this conn entry
	AddExchange(rtt time.Duration)

	// get the address that this connentry's connection is pointing to
	GetAddress() string

	// gets the weight of this conn entry, indicating how efficient it is
	GetWeight() (weight UpstreamWeight)

	// retrieves the underlying DNS connection for this entry
	GetConn() *dns.Conn

	// retrives the underlying upstream for this entry
	GetUpstream() (upstream *Upstream)

	// Closes the connection stored by this connentry
	Close()

	// Tells the conn entry to track a new error
	AddError()

	// Determines if the conn entry has seen any errors
	Error() bool
}

type ConnPool interface {
	// Retrieves a new connection from the pool
	// returns an upstream and a nil connentry if a new
	// connection must be made
	Get() (ce ConnEntry, upstream Upstream)

	// Adds a new connection to the pool
	Add(ce ConnEntry) (err error)

	// Add a new upstream to the pool
	AddUpstream(r *Upstream)

	// Close a given connection
	CloseConnection(ce ConnEntry)

	// Adds a new connection to the pool targetting a given upstream and using a given dial function to make the connection.
	// The abstraction of dialFunc is for dependency injection
	NewConnection(upstream Upstream, dialFunc DialFunc) (ce ConnEntry, err error)
	// Returns the number of open connections in the pool
	Size() int
}

type connPool struct {
	// List of actual upstream structs
	upstreams []*Upstream

	// List of upstream names.  This is kept separate so that callers
	// can iterate through the upstreams list by name without having
	// to lock the actual upstreams array.
	upstreamNames []UpstreamName

	cache map[string][]ConnEntry
	lock  Lock
}

type CachedConn interface {
	Close() error
}

type connEntry struct {
	// The actual connection
	Conn CachedConn

	// The upstream that this connection is associated with
	upstream Upstream

	// The total RTT for this connection
	totalRTT time.Duration

	// The total exchanges for this connection
	exchanges int

	// whether this connection hit an error
	error bool
}

type Lock struct {
	sync.RWMutex
	locklevel int
}

// flag this connentry as having errors
func (c *connEntry) AddError() {
	c.error = true
}

// retrieves error status
func (c connEntry) Error() bool {
	return c.error
}

// Increment the internal counters tracking successful exchanges and durations
func (c *connEntry) AddExchange(rtt time.Duration) {
	// TODO evaluate having multiple timeouts for dialing vs rtt'ing
	RttTimeout := GetConfiguration().Timeout
	if RttTimeout == 0 {
		RttTimeout = 500
	}
	// check to see if this exchange took too long
	if rtt > time.Duration(RttTimeout)*time.Millisecond {
		// next time around, treat this as a bogus connection
		c.AddError()
	}

	c.totalRTT += rtt
	c.exchanges += 1
}

func (c connEntry) GetAddress() string {
	return c.upstream.GetAddress()
}

func (c connEntry) Close() {
	c.Conn.Close()
}

func (c connEntry) GetConn() (conn *dns.Conn) {
	return c.Conn.(*dns.Conn)
}

func (c connEntry) GetUpstream() (upstream *Upstream) {
	return &c.upstream
}
func (ce connEntry) GetWeight() (weight UpstreamWeight) {
	if ce.totalRTT == 0 || ce.exchanges == 0 {
		// this connection hasn't seen any actual connection time, no weight
		weight = 0
	} else {
		weight = UpstreamWeight(ce.totalRTT/time.Millisecond) / UpstreamWeight(ce.exchanges)
	}
	Logger.Log(NewLogMessage(
		DEBUG,
		LogContext{
			"what":               "setting weight on connection",
			"connection_address": ce.GetAddress(),
			"currentRTT":         fmt.Sprintf("%s", ce.totalRTT),
			"exchanges":          fmt.Sprintf("%d", ce.exchanges),
			"new_weight":         fmt.Sprintf("%f", weight),
		},
		func() string { return fmt.Sprintf("upstream [%v] connection [%v]", ce.upstream, ce) },
	))
	return
}

func NewConnPool() *connPool {
	return &connPool{
		cache: make(map[string][]ConnEntry),
	}
}

func (c *connPool) Lock() {
	c.lock.Lock()
}

func (c *connPool) Unlock() {
	c.lock.Unlock()
}

// Retrieves a given upstream based on its address.  This is used to avoid excessive
// locking while passing around upstreams
func (c *connPool) getUpstreamByAddress(address string) (upstream *Upstream, err error) {
	// technically not the fastest way to do this, but the list of upstreams shouldn't be big enough for it to matter
	for _, upstream := range c.upstreams {
		if upstream.GetAddress() == address {
			return upstream, nil
		}
	}
	return &Upstream{}, fmt.Errorf("could not find upstream with address [%s]", address)
}

// WARNING: this function is not reentrant, it is meant to be called internally
// when the connection pool is already locked and needs to update its upstream weights
func (c *connPool) weightUpstream(upstream *Upstream, ce ConnEntry) {
	// the upstream weight will be the result of the most recent connection:
	// we want to prefer the upstream with the fastest connections and
	// ditch them when they start to slow down
	upstream.SetWeight(ce.GetWeight())

}

// Will select the lowest weighted cached connection
// falling back to the lowest weighted upstream if none exist
// upstreams that are cooling will be ignored
func (c *connPool) getBestUpstream() (upstream Upstream) {
	// get the first upstream with connections, default to the 0th connection on the list
	// iterate through in order; this list is sorted based on weight

	var bestCandidate Upstream

	// goal: find the highest lowest weighted upstream with connections
	for _, each := range c.upstreams {
		if conns, ok := c.cache[each.GetAddress()]; ok {
			// should this upstream be taking connections?
			if !each.IsCooling() {
				// it should. does it HAVE connections?
				if len(conns) > 0 {
					return *each
				}

				// it's open, but it doesn't have existing connections, let's
				// use it iff there's no existing connections
				if (bestCandidate == Upstream{}) {
					bestCandidate = *each
				}
			}
		}
	}

	// if we got here, there are no cached connections, did we find a candidate?
	// the candidate will be the lowest weighted connection that wasn't cooling
	if (bestCandidate != Upstream{}) {
		return bestCandidate
	}

	// no cached connections, everything is cooling, let's abuse the lowest
	// weighted upstream
	return *c.upstreams[0]
}

// arranges the upstreams based on weight
func (c *connPool) sortUpstreams() {
	sort.Slice(c.upstreams, func(i, j int) bool {
		return c.upstreams[i].GetWeight() < c.upstreams[j].GetWeight()
	})
}

func (c *connPool) updateUpstreamTelemetry(u Upstream, errorString string) {
	wut := u.WakeupTime()
	coolingString := fmt.Sprintf(
		"%d-%d-%d %d:%d:%d:%s",
		wut.Year(),
		wut.Month(),
		wut.Day(),
		wut.Hour(),
		wut.Minute(),
		wut.Second(),
		time.Now().Sub(wut),
	)
	UpstreamWeightGauge.WithLabelValues(u.GetAddress(), errorString, coolingString).Set(float64(u.GetWeight()))
}

// updates a upstream's weight based on a conn entry being added or closed
func (c *connPool) updateUpstream(ce ConnEntry) (err error) {
	address := ce.GetAddress()
	// get the actual pointer for this ce's upstream
	upstream, err := c.getUpstreamByAddress(address)
	if err != nil {
		return fmt.Errorf("could not update upstream with address [%s]: %s", address, err)
	}

	// we may need to update after a fresh connection errored out
	// so ignore weightless connections
	if ce.GetWeight() != 0 {
		c.weightUpstream(upstream, ce)
	}

	errorString := ""
	if upstream.IsCooling() {
		errorString = "cooling"
	}

	if ce.Error() {
		c.coolAndPurgeUpstream(upstream)
		errorString += " connection"
	}

	c.sortUpstreams()
	c.updateUpstreamTelemetry(*upstream, errorString)
	return nil
}

// adds a connection to the cache
func (c *connPool) Add(ce ConnEntry) (err error) {
	c.Lock()
	defer c.Unlock()

	address := ce.GetAddress()
	if err = c.updateUpstream(ce); err != nil {
		err = fmt.Errorf("couldn't update upstream weight on connection to [%s]: %s", address, err.Error())
	}

	Logger.Log(NewLogMessage(
		INFO,
		LogContext{
			"what":    "adding connection back to conn pool",
			"address": address,
		},
		func() string { return fmt.Sprintf("connection entry [%v]", ce) },
	))

	if _, ok := c.cache[address]; ok {
		c.cache[address] = append(c.cache[address], ce)
	} else {
		// the max is greater than zero and there's nothing here, so we can just insert
		c.cache[address] = []ConnEntry{ce}
	}

	ConnPoolSizeGauge.WithLabelValues(address).Set(float64(len(c.cache[address])))
	return
}

// Makes a new connection to a given upstream, wraps the whole thing in conn entry
func (c *connPool) NewConnection(upstream Upstream, dialFunc DialFunc) (ce ConnEntry, err error) {
	address := upstream.GetAddress()
	Logger.Log(NewLogMessage(
		INFO,
		LogContext{
			"what":    "making new connection",
			"weight":  fmt.Sprintf("%f", upstream.GetWeight()),
			"address": address,
			"next":    "dialing",
		},
		func() string { return fmt.Sprintf("upstream [%v] connpool [%v]", upstream, c) },
	))

	tlsTimer := prometheus.NewTimer(prometheus.ObserverFunc(func(v float64) {
		TLSTimer.WithLabelValues(address).Observe(v)
	}),
	)
	NewConnectionAttemptsCounter.WithLabelValues(address).Inc()
	conn, err := dialFunc(address)
	dialDuration := tlsTimer.ObserveDuration()
	if err != nil {
		// errors! better cool this upstream
		c.Lock()
		defer c.Unlock()

		upstream, upstreamErr := c.getUpstreamByAddress(address)
		if upstreamErr != nil {
			err = fmt.Errorf("could not retrieve upstream for %s: %s: %s", address, upstreamErr, err)
			return &connEntry{}, err
		}

		c.coolAndPurgeUpstream(upstream)
		c.updateUpstreamTelemetry(*upstream, "connection failure")
		FailedConnectionsCounter.WithLabelValues(address).Inc()
		return &connEntry{}, fmt.Errorf("cooling upstream, could not connect to [%s]: %s", address, err)
	}

	Logger.Log(NewLogMessage(
		INFO,
		LogContext{
			"what":    "connection successful",
			"address": address,
		},
		nil,
	))

	ce = &connEntry{Conn: conn, upstream: upstream}
	ce.AddExchange(dialDuration)
	return ce, nil
}

// attempts to retrieve a connection from the most attractive upstream
// if it doesn't have one, returns an upstream for the caller to connect to
func (c *connPool) Get() (ce ConnEntry, upstream Upstream) {
	c.Lock()
	defer c.Unlock()

	upstream = c.getBestUpstream()
	// now use the address for whichever one came out, the default one with no connections
	// or the best weighted upstream with cached connections
	address := upstream.GetAddress()

	// Check for an existing connection
	if conns, ok := c.cache[address]; ok && len(conns) > 0 {
		for i := 0; i < len(conns); i++ {
			j := i + 1
			// pop off a connection and return it
			ce, c.cache[address] = conns[i], conns[j:]
			ConnPoolSizeGauge.WithLabelValues(address).Set(float64(len(c.cache[address])))
			return ce, Upstream{}
		}
	}
	// we couldn't find a single connection, tell the caller to make a new one to the best weighted upstream
	return &connEntry{}, upstream
}

// since this reads all the maps, it needs to make sure there are no concurrent writes
// caveat emptor
func (c *connPool) Size() int {
	c.Lock()
	defer c.Unlock()

	size := 0
	for _, v := range c.cache {
		size = size + len(v)
	}
	return size
}

// maintains the internal list of upstreams that this connection pool
// will attempt to connect to
func (c *connPool) AddUpstream(r *Upstream) {
	c.upstreams = append(c.upstreams, r)
}

func (c *connPool) CloseConnection(ce ConnEntry) {
	c.Lock()
	defer c.Unlock()
	c.updateUpstream(ce)
	ce.Close()
}

// take an upstream pointer (so that we can update the actual record)
// and tell it to cool down, sever all connections
// non re-entrant, needs outside locking
func (c *connPool) coolAndPurgeUpstream(upstream *Upstream) {
	config := GetConfiguration()
	cooldownPeriod := time.Duration(500) * time.Millisecond
	if config.CooldownPeriod != 0 {
		cooldownPeriod = config.CooldownPeriod * time.Millisecond
	}

	upstream.Cooldown(cooldownPeriod)

	c.purgeUpstream(*upstream)
}

// closes all connections that belong to a given upstream
// non re-entrant
func (c *connPool) purgeUpstream(upstream Upstream) {
	addr := upstream.GetAddress()
	if _, ok := c.cache[addr]; ok {
		for _, conn := range c.cache[addr] {
			// run async so as not to block queries that might be calling
			go func(conn ConnEntry) {
				c.CloseConnection(conn)
			}(conn)
		}
		c.cache[addr] = []ConnEntry{}
	}
	ConnPoolSizeGauge.WithLabelValues(addr).Set(float64(len(c.cache[addr])))
}
