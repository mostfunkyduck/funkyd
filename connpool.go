package main

// Pools connections to upstream servers, does high level lifecycle management and
// prioritizes which upstreams get connections and which don't.
import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
)

type DialFunc func(address string) (*dns.Conn, error)

type ConnEntry interface {
	// Add an exchange with a duration to this conn entry
	AddExchange(rtt time.Duration)

	// get the address that this connentry's connection is pointing to
	GetAddress() string

	// returns the average latency of this connection as a time.Duration
	GetAverageLatency() time.Duration

	// gets the weight of this conn entry, indicating how efficient it is
	GetWeight() (weight UpstreamWeight)

	// retrieves the underlying DNS connection for this entry
	GetConn() *dns.Conn

	// retrives the underlying upstream for this entry
	GetUpstream() (upstream *Upstream)

	// Closes the connection stored by this connentry
	Close()

	// indicates that when this connection closes, it should cool the upstream
	CoolUpstream()
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

	// Adds a new connection to the pool targeting a given upstream and using a given dial function
	// to make the connection.
	// The abstraction of dialFunc is for dependency injection
	NewConnection(upstream Upstream, dialFunc DialFunc) (ce ConnEntry, err error)
	// Returns the number of open connections in the pool
	Size() int
}

type connPool struct {
	// List of actual upstream structs
	upstreams []*Upstream

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

	// whether the upstream for this connection should be cooled when the connection closes
	coolUpstream bool
}

type Lock struct {
	sync.RWMutex
}

// Increment the internal counters tracking successful exchanges and durations.
func (c *connEntry) AddExchange(rtt time.Duration) {
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

func (c *connEntry) CoolUpstream() {
	c.coolUpstream = true
}

// Returns the average latency of this connection in time.Duration, defined as total RTT/exchanges.
func (c connEntry) GetAverageLatency() time.Duration {
	if c.exchanges == 0 {
		return 0
	}
	return c.totalRTT / time.Duration(c.exchanges)
}

func (c connEntry) GetWeight() (weight UpstreamWeight) {
	if c.totalRTT == 0 || c.exchanges == 0 {
		// this connection hasn't seen any actual connection time, no weight
		weight = 0
	} else {
		weight = UpstreamWeight(c.totalRTT/time.Millisecond) / UpstreamWeight(c.exchanges)
	}
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
// locking while passing around upstreams.
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
// when the connection pool is already locked and needs to update its upstream weights.
func (c *connPool) weightUpstream(upstream *Upstream, ce ConnEntry) {
	// the upstream weight will be the result of the most recent connection:
	// we want to prefer the upstream with the fastest connections and
	// ditch them when they start to slow down
	upstream.SetWeight(ce.GetWeight())
}

// Will select the lowest weighted cached connection
// falling back to the lowest weighted upstream if none exist
// upstreams that are cooling will be ignored.
func (c *connPool) getBestUpstream() (upstream Upstream) {
	// get the first upstream with connections, default to the 0th connection on the list
	// iterate through in order; this list is sorted based on weight

	var bestCandidate Upstream

	// goal: find the highest lowest weighted upstream with connections
	for _, each := range c.upstreams {
		// nolint
		if conns, ok := c.cache[each.GetAddress()]; ok {
			// should this upstream be taking connections?
			if !each.IsCooling() {
				// the upstream is operational, use it immediately if it has connections or if we
				// haven't used it yet
				if len(conns) > 0 || each.GetWeight() == 0 {
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

// arranges the upstreams based on weight.
func (c *connPool) sortUpstreams() {
	sort.Slice(c.upstreams, func(i, j int) bool {
		return c.upstreams[i].GetWeight() < c.upstreams[j].GetWeight()
	})
}

func (c *connPool) updateUpstreamTelemetry(u Upstream, errorString string) {
	coolingString := "no cooling"
	wut := u.WakeupTime()
	if time.Now().Before(wut) {
		coolingString = fmt.Sprintf(
			"%d-%d-%d %d:%d:%d:%s",
			wut.Year(),
			wut.Month(),
			wut.Day(),
			wut.Hour(),
			wut.Minute(),
			wut.Second(),
			time.Since(wut),
		)
	}
	UpstreamWeightGauge.WithLabelValues(u.GetAddress(), errorString, coolingString).Set(float64(u.GetWeight()))
}

// updates a upstream's weight based on a conn entry being added or closed.
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

	timeout := GetConfiguration().SlowConnectionThreshold
	if timeout == 0 {
		timeout = 500
	}

	// check to see if this exchange took too long
	errorString := ""
	avgRTT := ce.GetAverageLatency()
	if avgRTT > time.Duration(timeout)*time.Millisecond {
		c.coolAndPurgeUpstream(upstream)
		errorString += "exceeded timeout, started cooling"
	}

	// now that the upstream is weighted, sort it
	c.sortUpstreams()
	c.updateUpstreamTelemetry(*upstream, errorString)
	return nil
}

// adds a connection to the cache.
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

	c.cache[address] = append(c.cache[address], ce)

	ConnPoolSizeGauge.WithLabelValues(address).Set(float64(len(c.cache[address])))
	return
}

// Makes a new connection to a given upstream, wraps the whole thing in conn entry.
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
		// errors! better cool this upstream (TODO find a way to have this share code with regular conn closing)
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
// if it doesn't have one, returns an upstream for the caller to connect to.
func (c *connPool) Get() (ce ConnEntry, upstream Upstream) {
	c.Lock()
	defer c.Unlock()

	upstream = c.getBestUpstream()
	// now use the address for whichever one came out, the default one with no connections
	// or the best weighted upstream with cached connections
	address := upstream.GetAddress()

	// Check for an existing connection
	if conns, ok := c.cache[address]; ok && len(conns) > 0 {
		// pop off a connection and return it
		ce, c.cache[address] = conns[0], conns[1:]
		ConnPoolSizeGauge.WithLabelValues(address).Set(float64(len(c.cache[address])))
		return ce, Upstream{}
	}
	// we couldn't find a single connection, tell the caller to make a new one to the best weighted upstream
	return &connEntry{}, upstream
}

// since this reads all the maps, it needs to make sure there are no concurrent writes
// caveat emptor.
func (c *connPool) Size() int {
	c.Lock()
	defer c.Unlock()

	size := 0
	for _, v := range c.cache {
		size += len(v)
	}
	return size
}

// maintains the internal list of upstreams that this connection pool
// will attempt to connect to.
func (c *connPool) AddUpstream(r *Upstream) {
	c.upstreams = append(c.upstreams, r)
}

func (c *connPool) CloseConnection(ce ConnEntry) {
	c.Lock()
	defer c.Unlock()
	if err := c.updateUpstream(ce); err != nil {
		Logger.Log(LogMessage{
			Level: ERROR,
			Context: LogContext{
				"what":  "error updating upstream during connection closure",
				"error": err.Error(),
				"next":  "proceeding to close connection",
			},
		})
	}
	ce.Close()
}

// take an upstream pointer (so that we can update the actual record)
// and tell it to cool down, sever all connections
// non re-entrant, needs outside locking.
func (c *connPool) coolAndPurgeUpstream(upstream *Upstream) {
	cooldownPeriod := GetConfiguration().CooldownPeriod * time.Millisecond
	if cooldownPeriod == 0 {
		// nolint
		cooldownPeriod = time.Duration(500) * time.Millisecond
	}

	upstream.Cooldown(cooldownPeriod)

	c.purgeUpstream(*upstream)
}

// closes all connections that belong to a given upstream
// non re-entrant.
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
