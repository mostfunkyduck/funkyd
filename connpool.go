package main

import (
	"fmt"
	"github.com/mostfunkyduck/dns"
)

// TODO link that project which sorta inspired this
type ConnPool struct {
	cache           map[string][]*ConnEntry
	lock            Lock
	MaxConnsPerHost int
}

type ConnEntry struct {
	Conn    *dns.Conn
	Address string
}

func InitConnPool() ConnPool {
	config := GetConfiguration()
	return ConnPool{
		cache:           make(map[string][]*ConnEntry),
		MaxConnsPerHost: config.MaxConnsPerHost,
	}
}
func (c *ConnPool) Lock() {
	c.lock.Lock()
}

func (c *ConnPool) Unlock() {
	c.lock.Unlock()
}

// adds a connection to the cache, returns whether or not it could be added and an error
func (c *ConnPool) Add(ce *ConnEntry) (bool, error) {
	c.Lock()
	defer c.Unlock()
	if _, ok := c.cache[ce.Address]; ok {
		// this way, the 0 value will essentially disable building the connection pool
		if len(c.cache[ce.Address]) >= c.MaxConnsPerHost {
			return false, nil
		}
		c.cache[ce.Address] = append(c.cache[ce.Address], ce)
	} else {
		c.cache[ce.Address] = []*ConnEntry{ce}
	}
	ConnPoolSizeGauge.WithLabelValues(ce.Address).Set(float64(len(c.cache[ce.Address])))
	return true, nil
}

func (c *ConnPool) Get(address string) (*ConnEntry, error) {
	c.Lock()
	defer c.Unlock()
	var ret *ConnEntry
	// Check for an existing connection
	if conns, ok := c.cache[address]; ok {
		if len(conns) > 0 {
			// pop off a connection and return it
			ret, c.cache[address] = conns[0], conns[1:]
			ConnPoolSizeGauge.WithLabelValues(address).Set(float64(len(c.cache[address])))
			return ret, nil
		}
	}
	return &ConnEntry{}, fmt.Errorf("could not retrieve connection for [%s] from cache", address)
}
