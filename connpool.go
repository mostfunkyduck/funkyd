package main

import (
	"fmt"
	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
	"sort"
	"time"
)


// Increment the internal counters tracking successful exchanges and durations
func (c *ConnEntry) AddExchange(rtt time.Duration) {
	c.totalRTT += rtt
	c.exchanges += 1
}

func (c *ConnEntry) GetAddress() string {
	return c.upstream.GetAddress()
}

func (ce *ConnEntry) GetWeight() (weight UpstreamWeight) {
	// it should be that (0 < weight < 1) in most normal circumstances,
	// if weight goes over 1, it means that the total exchanges is higher than the total
	// number of ms that the connection has been active - not a normal situation unless
	// the connection is blazing fast
	currentRTT := UpstreamWeight(ce.totalRTT * time.Millisecond)
	if currentRTT == 0 {
		// this connection hasn't seen any actual connection time, no weight
		return 0
	}
	return UpstreamWeight(ce.exchanges) / currentRTT
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
	upstream.Weight = ce.GetWeight()
	UpstreamWeightGauge.WithLabelValues(upstream.GetAddress()).Set(float64(upstream.Weight))
}

// arranges the upstreams based on weight
func (c *ConnPool) sortUpstreams() {
	sort.SliceStable(c.upstreams, func(i, j int) bool {
		return c.upstreams[i].Weight < c.upstreams[j].Weight
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

// adds a connection to the cache, returns whether or not it could be added and an error
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
	// TODO handle error, it can't stop things, but we should know

	if _, ok := c.cache[address]; ok {
		c.cache[address] = append(c.cache[address], ce)
	} else {
		// the max is greater than zero and there's nothing here, so we can just insert
		c.cache[address] = []*ConnEntry{ce}
	}
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
			"what": fmt.Sprintf("connection to %s successful", address),
		},
		nil,
	))

	ce = &ConnEntry{Conn: conn, upstream: upstream}
	ce.AddExchange(dialDuration)
	return ce, nil
}

func (c *ConnPool) getBestUpstream() (upstream Upstream) {
	// get the first upstream with connections, default to the 0th connection on the list
	// iterate through in order since this list is sorted based on weight
	upstream = *c.upstreams[0] // start off with the default
	for _, each := range c.upstreams {
		if conns, ok := c.cache[each.GetAddress()]; ok && len(conns) > 0 {
			// there were connections here, update upstream to be the one we want
			return *each
		}
	}
	return upstream
}

func (c *ConnPool) Get() (ce *ConnEntry, upstream Upstream, err error) {
	c.Lock()
	defer c.Unlock()

	upstream = c.getBestUpstream()
	// now use the address for whichever one came out, the default one with no connections
	// or the best weighted upstream with cached connections
	address := upstream.GetAddress()

	// Check for an existing connection
	if conns, ok := c.cache[address]; ok {
		if len(conns) > 0 {
			for i := 0; i < len(conns); i++ {
				j := i + 1
				// pop off a connection and return it
				ce, c.cache[address] = conns[i], conns[j:]
				ConnPoolSizeGauge.WithLabelValues(address).Set(float64(len(c.cache[address])))
				return ce, Upstream{}, nil
			}
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
	c.upstreamNames = append(c.upstreamNames, r.Name)
}

func (c *ConnPool) CloseConnection(ce *ConnEntry) {
	c.Lock()
	defer c.Unlock()
	c.updateUpstream(ce)
	ce.Conn.Close()
}
