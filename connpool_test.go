package main

import (
	"fmt"
	"github.com/stretchr/testify/mock"
	"github.com/miekg/dns"
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
	pool.AddResolver(
		&Resolver {
			Name: "example.com",
			Port: 12345,
		},
	)
	return &pool
}

func TestConnectionPoolSingleEntry(t *testing.T) {
	pool := buildPool()
	ce, err := pool.NewConnection(*pool.resolvers[0], func (addr string) (*dns.Conn, error) {
		return &dns.Conn{}, nil
	})
	if err != nil {
		t.Fatalf("could not make connection to resolver [%v]: %s", pool.resolvers[0], err.Error())
	}
	err = pool.Add(ce)
	if err != nil {
		t.Errorf("failed to add connection entry [%v] to pool [%v] - [%s]\n", ce, pool, err)
	}

	size := pool.Size()
	if size != 1 {
		t.Errorf("pool [%v] had incorrect size [%d] after adding ce [%v], expected %d", pool, size, ce, 1)
	}

	ce1, res, err := pool.Get()
	if err != nil {
		t.Fatalf("failed to retrieve new connection from pool [%v]", pool)
	}

	if (res != Resolver{}) {
		t.Fatalf("tried to retrieve connection from pool, was prompted to make a new connection instead, resolver was [%v]", res)
	}

	ce1Address := ce1.GetAddress()
	ceAddress := ce.GetAddress()
	if ce1Address != ceAddress {
		t.Errorf("retrieved entry's address [%s] was not equal to inserted entry's [%s]", ce1Address, ceAddress)
	}

	size = pool.Size()
	if size != 0 {
		t.Errorf("pool [%v] had incorrect size [%d] after getting ce [%v], expected %d", pool, size, ce, 0)
	}
}

func TestConnectionPoolMultipleAddresses(t *testing.T) {
	pool := buildPool()
	resolverNamesSeen := make(map[ResolverName]bool)
	max := 10
	f := func(i int) ResolverName {
		return ResolverName(fmt.Sprintf("entry #%d", i))
	}

	for i := 0; i < max; i++ {
			name := f(i)
			pool.AddResolver(&Resolver{
				Name: ResolverName(f(i)),
			})
			resolverNamesSeen[name] = false
	}

	for _, each := range pool.resolvers {
		address := each.GetAddress()
		ce, err := pool.NewConnection(*each, func (addr string) (*dns.Conn, error){
			return &dns.Conn{}, nil
		})
		if err != nil {
			t.Fatalf("could not get new connection on address [%s] resolver [%v]: %s", address, each, err.Error())
		}

		if err := pool.Add(ce); err != nil {
			t.Fatalf("error inserting entry [%v] for resolver [%v] on address [%s]: %s", ce, each, each.GetAddress(), err.Error())
		}
	}

	for i := 0; i < max; i++ {
		ce, res, err := pool.Get()
		if err != nil {
			t.Fatalf("error retrieving entry [%d] from pool [%v]: [%s]", i, pool, err)
		}

		if (res != Resolver{}) {
			t.Fatalf("tried to retrieve connection from pool, got prompted to make a connection instead. size: [%d], pool: [%v], res: [%v] entry: [%d]", pool.Size(), pool, res, i)
		}

		resolverNamesSeen[ce.resolver.Name] = true
	}

	for name, seen := range resolverNamesSeen {
		if !seen {
			t.Fatalf("[%s] was inserted into the pool, but was not taken out while getting all connections from the pool! this means that the pool is prompting for new connections before it finishes cleaning out existing ones", name)
		}
	}
}

func TestIllegalResolverAddition(t *testing.T) {
	pool := buildPool()
	ce := &ConnEntry {
		resolver: Resolver {
			Name: "doesntexist",
		},
	}

	if err := pool.Add(ce); err == nil {
		t.Fatalf("was able to add conn entry [%v] with non-existant resolver [%v] to pool [%v]", ce, ce.resolver, pool)
	}
}

func TestConnectionPoolSize(t *testing.T) {
	pool := buildPool()
	max := 10
	f := func(idx int) *ConnEntry {
		res := Resolver { Name: "example.com", Port: idx}
		return &ConnEntry{resolver: res}
	}
	// we want to add distinct entries in different sub-pools, based on the current
	// implementation which stores connections separately for each host
	for i := 0; i < max; i++ {
		ce := f(i)
		pool.AddResolver(&ce.resolver)
		for j := 0; j < max; j++ {
			if err := pool.Add(ce); err != nil {
				t.Fatalf("tried to add connection [%v] to pool [%v], got potential error [%s]", ce, pool, err)
			}
		}
	}

	if pool.Size() != max * max {
		t.Fatalf("got the wrong size for the pool, expected [%d], got [%d]", max * max, pool.Size())
	}
}
