package main

import (
	"fmt"
	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
	"sort"
	"sync"
	"time"
)

type ConnPool struct {
	// List of actual upstream structs
	upstreams []*Upstream

	// List of upstream names.  This is kept separate so that callers
	// can iterate through the upstreams list by name without having
	// to lock the actual upstreams array.
	upstreamNames []UpstreamName

	cache map[string][]*ConnEntry
	lock  Lock
}

type CachedConn interface {
	Close() error
}

type ConnEntry struct {
	// The actual connection
	Conn CachedConn

	// The upstream that this connection is associated with
	upstream Upstream

	// The total RTT for this connection
	totalRTT time.Duration

	// The total exchanges for this connection
	exchanges int
}

type Lock struct {
	sync.RWMutex
	locklevel int
}

// Increment the internal counters tracking successful exchanges and durations
func (c *ConnEntry) AddExchange(rtt time.Duration) {
	c.totalRTT += rtt
	c.exchanges += 1
}

func (c *ConnEntry) GetAddress() string {
	return c.upstream.GetAddress()
}

func (ce *ConnEntry) GetWeight() (weight UpstreamWeight) {
	currentRTT := UpstreamWeight(ce.totalRTT / time.Millisecond)
	if currentRTT == 0.0 || ce.exchanges == 0 {
		// this connection hasn't seen any actual connection time, no weight
		weight = 0
	} else {
		weight = currentRTT / UpstreamWeight(ce.exchanges)
	}
	Logger.Log(NewLogMessage(
		INFO,
		LogContext{
			"what":               "setting weight on connection",
			"connection_address": ce.GetAddress(),
			"currentRTT":         fmt.Sprintf("%f", currentRTT),
			"exchanges":          fmt.Sprintf("%f", UpstreamWeight(ce.exchanges)),
			"new_weight":         fmt.Sprintf("%f", weight),
		},
		func() string { return fmt.Sprintf("upstream [%v] connection [%v]", ce.upstream, ce) },
	))
	return
}

func NewConnPool() *ConnPool {
	return &ConnPool{
		cache: make(map[string][]*ConnEntry),
	}
}

func (c *ConnPool) Lock() {
	c.lock.Lock()
}

func (c *ConnPool) Unlock() {
	c.lock.Unlock()
}

// Retrieves a given upstream based on its address.  This is used to avoid excessive
// locking while passing around upstreams
func (c *ConnPool) GetUpstreamByAddress(address string) (upstream *Upstream, err error) {
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
func (c *ConnPool) weightUpstream(upstream *Upstream, ce ConnEntry) {
	// the upstream weight will be the result of the most recent connection:
	// we want to prefer the upstream with the fastest connections and
	// ditch them when they start to slow down
	upstream.SetWeight(ce.GetWeight())
	UpstreamWeightGauge.WithLabelValues(upstream.GetAddress()).Set(float64(upstream.GetWeight()))
}

//  Will select the lowest weighted cached connection
// falling back to the lowest weighted upstream if none exist
// will also clean up and close slow connections
func (c *ConnPool) getBestUpstream() (upstream Upstream) {
	// get the first upstream with connections, default to the 0th connection on the list
	// iterate through in order; this list is sorted based on weight
	upstream = *c.upstreams[0] // start off with the default
	// goal: find the highest lowest weighted upstream with connections
	for _, each := range c.upstreams {
		if conns, ok := c.cache[each.GetAddress()]; ok && len(conns) > 0 {
			// is this upstream's weight more than double the best upstream?
			// there were connections here, let's reuse them
			return *each
		}
	}
	return upstream
}

// arranges the upstreams based on weight
func (c *ConnPool) sortUpstreams() {
	sort.Slice(c.upstreams, func(i, j int) bool {
		return c.upstreams[i].GetWeight() < c.upstreams[j].GetWeight()
	})
}

// updates a upstream's weight based on a conn entry being added or closed
func (c *ConnPool) updateUpstream(ce *ConnEntry) (err error) {
	address := ce.GetAddress()
	// get the actual pointer for this ce's upstream
	upstream, err := c.GetUpstreamByAddress(address)
	if err != nil {
		return fmt.Errorf("could not add conn entry with address [%s]: %s", address, err.Error())
	}

	c.weightUpstream(upstream, *ce)

	c.sortUpstreams()
	return
}

// iterate through the cache and close connections to slow upstreams
func (c *ConnPool) closeSlowUpstreams() {
	upstream := *c.upstreams[0]
	for _, each := range c.upstreams {
		if conns, ok := c.cache[each.GetAddress()]; ok && len(conns) > 0 {
			if upstream.GetWeight()*2 < each.GetWeight() {
				// this upstream is way too heavy, and all the previous ones had no connections
				// close all it's connections and let it cool down until we need it
				for _, conn := range conns {
					conn.Conn.Close()
				}
				c.cache[each.GetAddress()] = []*ConnEntry{}
			}
		}
	}
}

// adds a connection to the cache
func (c *ConnPool) Add(ce *ConnEntry) (err error) {
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
		c.cache[address] = []*ConnEntry{ce}
	}
	c.closeSlowUpstreams()
	ConnPoolSizeGauge.WithLabelValues(address).Set(float64(len(c.cache[address])))
	return
}

// Makes a new connection to a given upstream, wraps the whole thing in conn entry
func (c *ConnPool) NewConnection(upstream Upstream, dialFunc func(address string) (*dns.Conn, error)) (ce *ConnEntry, err error) {
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
		Logger.Log(NewLogMessage(
			ERROR,
			LogContext{
				"what":    "connection error",
				"address": address,
				"error":   err.Error(),
			},
			nil,
		))
		FailedConnectionsCounter.WithLabelValues(address).Inc()
		return &ConnEntry{}, err
	}

	Logger.Log(NewLogMessage(
		INFO,
		LogContext{
			"what":    "connection successful",
			"address": address,
		},
		nil,
	))

	ce = &ConnEntry{Conn: conn, upstream: upstream}
	ce.AddExchange(dialDuration)
	return ce, nil
}

// attempts to retrieve a connection from the most attractive upstream
// if it doesn't have one, returns an upstream for the caller to connect to
func (c *ConnPool) Get() (ce *ConnEntry, upstream Upstream, err error) {
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
			return ce, Upstream{}, nil
		}
	}
	// we couldn't find a single connection, tell the caller to make a new one to the best weighted upstream
	return &ConnEntry{}, upstream, nil
}

// since this reads all the maps, it needs to make sure there are no concurrent writes
// caveat emptor
func (c *ConnPool) Size() int {
	c.Lock()
	defer c.Unlock()

	size := 0
	for _, v := range c.cache {
		size = size + len(v)
	}
	return size
}

func (c *ConnPool) AddUpstream(r *Upstream) {
	c.upstreams = append(c.upstreams, r)
}

func (c *ConnPool) CloseConnection(ce *ConnEntry) {
	c.Lock()
	defer c.Unlock()
	c.updateUpstream(ce)
	ce.Conn.Close()
}
