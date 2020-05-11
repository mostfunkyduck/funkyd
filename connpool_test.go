package main

import (
	"fmt"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/mock"
	"testing"
)

type MockConn struct {
	mock.Mock
}

func (m *MockConn) Close() error {
	ret := m.Called()
	return ret.Error(0)
}
func buildPool() *ConnPool {
	pool := InitConnPool()
	return &pool
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

	size := pool.Size()
	if size != 1 {
		t.Errorf("pool [%v] had incorrect size [%d] after adding ce [%v], expected %d", pool, size, ce, 1)
	}

	ce1, err := pool.Get(address)
	if err != nil {
		t.Errorf("failed to retrieve address [%s]: [%s]", address, err)
	}

	if !cmp.Equal(ce1, ce) {
		t.Errorf("retrieved entry [%v] was not equal to inserted entry [%v]", ce1, ce)
	}

	size = pool.Size()
	if size != 0 {
		t.Errorf("pool [%v] had incorrect size [%d] after getting ce [%v], expected %d", pool, size, ce, 0)
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

func TestConnectionPoolSize(t *testing.T) {
	pool := buildPool()
	f := func(idx int) *ConnEntry { return &ConnEntry{Address: fmt.Sprintf("example.com:%d", idx)} }
	// we want to add distinct entries in different sub-pools, based on the current
	// implementation which stores connections separately for each host
	for i := 0; i < 10; i++ {
		ce := f(i)
		for j := 0; j < 10; j++ {
			added, err := pool.Add(ce)
			if !added || err != nil {
				t.Fatalf("tried to add connection [%v] to pool [%v], got potential error [%s]", ce, pool, err)
			}
		}
	}

	if pool.Size() != 100 {
		t.Fatalf("got the wrong size for the pool, expected [100], got [%d]", pool.Size())
	}
}
