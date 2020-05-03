package main

import (
	"fmt"
	"github.com/google/go-cmp/cmp"
	"testing"
)

func buildPool() *ConnPool {
	// MAYBE make this a central function
	return &ConnPool{cache: make(map[string][]*ConnEntry)}
}

func TestConnectionPoolSingleEntry(t *testing.T) {
	pool := buildPool()
	address := "example.com"
	ce := &ConnEntry{Address: address}
	added, err := pool.Add(ce)
	if err != nil {
		t.Errorf("failed to add connection entry [%v] to pool [%v] - [%s]\n", ce, pool, err)
	}

	if !added {
		t.Errorf("connection pool [%v] rejected addition of connection [%v]", pool, ce)
	}

	ce1, err := pool.Get(address)
	if err != nil {
		t.Errorf("failed to retrieve address [%s]: [%s]", address, err)
	}

	if !cmp.Equal(ce1, ce) {
		t.Errorf("retrieved entry [%v] was not equal to inserted entry [%v]", ce1, ce)
	}
}

func TestConnectionPoolMultipleAddresses(t *testing.T) {
	pool := buildPool()
	max := 10
	f := func(i int) string {
		return fmt.Sprintf("entry #%d", i)
	}
	for i := 0; i < max; i++ {
		ce := &ConnEntry{
			Address: f(i),
		}
		added, err := pool.Add(ce)
		if err != nil {
			t.Errorf("error inserting entry %v", ce)
		}

		if !added {
			t.Errorf("connection pool [%v] rejected addition of connection [%v]", pool, ce)
		}
	}

	for i := 0; i < max; i++ {
		retrievedAddress := f(i)
		ce, err := pool.Get(retrievedAddress)
		if err != nil {
			t.Errorf("error retrieving entry [%d] from pool [%v]: [%s]", i, pool, err)
		}

		if ce.Address != retrievedAddress {
			t.Errorf("attempted to retrieve entry with address [%s], got [%s]", retrievedAddress, ce.Address)
		}
	}
}
