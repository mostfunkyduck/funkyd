package main

import (
	"fmt"
	"time"
)

// TODO link that project which sorta inspired this
func (c *ConnEntry) IsExpired() bool {
	if (c.ExpirationDate == time.Time{}) {
		return false
	}
	return time.Now().After(c.ExpirationDate)
}
func InitConnPool() ConnPool {
	return ConnPool{
		cache: make(map[string][]*ConnEntry),
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
		c.cache[ce.Address] = append(c.cache[ce.Address], ce)
	} else {
		// the max is greater than zero and there's nothing here, so we can just insert
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
			for i := 0; i < len(conns); i++ {
				j := i + 1
				// pop off a connection and return it
				ret, c.cache[address] = conns[i], conns[j:]
				ConnPoolSizeGauge.WithLabelValues(address).Set(float64(len(c.cache[address])))

				// if it's expired, close it and keep going
				if ret.IsExpired() {
					Logger.Log(NewLogMessage(
						INFO,
						LogContext{
							"what": fmt.Sprintf("connection to address [%s] has expired", address),
							"next": "closing connection",
						},
						"",
					))
					ret.Conn.Close()
					continue
				}

				return ret, nil
			}
		}
	}
	return &ConnEntry{}, fmt.Errorf("could not retrieve connection for [%s] from cache", address)
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
