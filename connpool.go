package main

import (
	"fmt"
	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
	"sort"
	"time"
)

// TODO link that project which sorta inspired this

func (c *ConnEntry) UpdateRTT(rtt time.Duration) {
	c.totalRTT += rtt
}

func (c *ConnEntry) GetAddress() string {
	return c.resolver.GetAddress()
}

func (ce *ConnEntry) GetWeight() (weight ResolverWeight) {
	currentRTT := int64(ce.totalRTT * time.Millisecond)
	if currentRTT == 0 {
		// this is the highest possible weight, this connection hasn't seen any action and will
		// jump to the top
		return 0
	}
	return ResolverWeight(int64(ce.exchanges) / currentRTT)
}

func InitConnPool() *ConnPool {
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

// Retrieves a given resolver based on its address.  This is used to avoid excessive
// locking while passing around resolvers
func (c *ConnPool) GetResolverByAddress(address string) (resolver *Resolver, err error) {
	// technically not the fastest way to do this, but the list of resolvers shouldn't be big enough for it to matter
	for _, res := range c.resolvers {
		if res.GetAddress() == address {
			return res, nil
		}
	}
	return &Resolver{}, fmt.Errorf("could not find resolver with address [%s]", address)
}

// WARNING: this function is not reentrant, it is meant to be called internally
// when the connection pool is already locked and needs to update its resolver weights
func (c *ConnPool) weightResolver(res *Resolver, ce ConnEntry) {
	res.Weight = ce.GetWeight()
}

// arranges the resolvers based on weight
func (c *ConnPool) sortResolvers() {
	sort.SliceStable(c.resolvers, func(i, j int) bool {
		return c.resolvers[i].Weight < c.resolvers[j].Weight
	})
}

// updates a resolver's weight based on a conn entry being added or closed
func (c *ConnPool) updateResolver(ce *ConnEntry) (err error) {
	address := ce.GetAddress()
	res, err := c.GetResolverByAddress(address)
	if err != nil {
		return fmt.Errorf("could not add conn entry with address [%s]: %s", address, err.Error())
	}

	c.weightResolver(res, *ce)

	c.sortResolvers()
	return
}

// adds a connection to the cache, returns whether or not it could be added and an error
func (c *ConnPool) Add(ce *ConnEntry) (err error) {
	c.Lock()
	defer c.Unlock()

	address := ce.GetAddress()
	c.updateResolver(ce)
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

// Makes a new connection to a given resolver, wraps the whole thing in conn entry
func (c *ConnPool) NewConnection(res Resolver, dialFunc func(address string) (*dns.Conn, error)) (ce *ConnEntry, err error) {
	address := res.GetAddress()
	Logger.Log(NewLogMessage(
		INFO,
		LogContext{
			"what":    "making new connection",
			"address": address,
			"next":    "dialing",
		},
		func() string { return fmt.Sprintf("res [%v] connpool [%v]", res, c) },
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

	return &ConnEntry{Conn: conn, resolver: res, totalRTT: dialDuration}, nil
}

func (c *ConnPool) getNextAddress() (address string) {
	return c.resolvers[0].GetAddress()
}

func (c *ConnPool) getBestResolver() (res Resolver) {
	// get the first resolver with connections, default to the 0th connection on the list
	// iterate through in order since this list is sorted based on weight
	res = *c.resolvers[0] // start off with the default
	for _, each := range c.resolvers {
		if conns, ok := c.cache[each.GetAddress()]; ok && len(conns) > 0 {
			// there were connections here, update res to be the one we want
			res = *each
		}
	}
	return
}

func (c *ConnPool) Get() (ce *ConnEntry, res Resolver, err error) {
	c.Lock()
	defer c.Unlock()

	res = c.getBestResolver()
	// now use the address for whichever one came out, the default one with no connections
	// or the best weighted resolver with cached connections
	address := res.GetAddress()

	// Check for an existing connection
	if conns, ok := c.cache[address]; ok {
		if len(conns) > 0 {
			for i := 0; i < len(conns); i++ {
				j := i + 1
				// pop off a connection and return it
				ce, c.cache[address] = conns[i], conns[j:]
				ConnPoolSizeGauge.WithLabelValues(address).Set(float64(len(c.cache[address])))
				ce.exchanges += 1
				return ce, Resolver{}, nil
			}
		}
	}
	// we couldn't find a single connection, tell the caller to make a new one to the best weighted resolver
	return &ConnEntry{}, *c.resolvers[0], nil
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

func (c *ConnPool) AddResolver(r *Resolver) {
	c.resolvers = append(c.resolvers, r)
	c.resolverNames = append(c.resolverNames, r.Name)
}

func (c *ConnPool) GetResolverNames() (names []ResolverName) {
	return c.resolverNames
}

func (c *ConnPool) CloseConnection(ce *ConnEntry) {
	c.Lock()
	defer c.Unlock()
	c.updateResolver(ce)
	ce.Conn.Close()
}
