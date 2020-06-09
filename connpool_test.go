package main

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func buildPool() ConnPool {
	pool := NewConnPool()
	pool.AddUpstream(
		&Upstream{
			Name: "example.com",
			Port: 12345,
		},
	)
	return pool
}

func TestConnectionPoolSingleEntry(t *testing.T) {
	pool := buildPool()
	var upstream Upstream
	x, upstream := pool.Get()
	if (upstream == Upstream{}) {
		t.Fatalf("could not retrieve upstream to connect to: [%v] [%v]", x, upstream)
	}
	pool.AddUpstream(&upstream)

	ce, err := pool.NewConnection(upstream, func(addr string) (*dns.Conn, error) {
		server, client := net.Pipe()
		server.Close()
		return &dns.Conn{Conn: client}, nil
	})
	if err != nil {
		t.Fatalf("could not make connection to upstream [%v]: %s", upstream, err.Error())
	}
	err = pool.Add(ce)
	if err != nil {
		t.Errorf("failed to add connection entry [%v] to pool [%v] - [%s]\n", ce, pool, err)
	}

	size := pool.Size()
	if size != 1 {
		t.Errorf("pool [%v] had incorrect size [%d] after adding ce [%v], expected %d", pool, size, ce, 1)
	}

	ce1, upstream := pool.Get()

	if (upstream != Upstream{}) {
		t.Fatalf("tried to retrieve connection from pool, was prompted to make a new connection instead, upstream was [%v]",
			upstream)
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
	pool := NewConnPool()
	upstreamNamesSeen := make(map[UpstreamName]bool)
	max := 10
	f := func(i int) UpstreamName {
		return UpstreamName(fmt.Sprintf("entry #%d", i))
	}

	var upstreams = make([]*Upstream, 0)
	for i := 0; i < max; i++ {
		name := f(i)
		newUpstream := &Upstream{
			Name: name,
		}
		pool.AddUpstream(newUpstream)
		upstreamNamesSeen[name] = false
		upstreams = append(upstreams, newUpstream)
	}

	var i int
	for _, each := range upstreams {
		// first, test that the pool prompts for new connections if it has
		// upstreams with no connections, but an initial weight of 0 - meaning
		// that it hasn't been tried yet
		if ce, upstream := pool.Get(); (upstream == Upstream{}) {
			t.Fatalf("connection pool provided a cached connection even though there were untested upstreams configured."+
				"pool: [%v], ce: [%v], upstream: [%v], i [%d]",
				pool, ce, upstream, i)
		}
		address := each.GetAddress()
		ce, err := pool.NewConnection(*each, func(addr string) (*dns.Conn, error) {
			server, client := net.Pipe()
			server.Close()
			return &dns.Conn{Conn: client}, nil
		})
		if err != nil {
			t.Fatalf("could not get new connection on address [%s] upstream [%v]: %s", address, each, err.Error())
		}

		// weight this connection so that its upstream doesn't look untested
		// adding 1 because on i = 0 it won't add any weight
		ce.AddExchange(time.Duration(i+1) * time.Minute)
		if err := pool.Add(ce); err != nil {
			t.Fatalf("error inserting entry [%v] for upstream [%v] on address [%s]: %s", ce, each, address, err.Error())
		}
		i++
	}

	for i := 0; i < max; i++ {
		ce, upstream := pool.Get()

		if (upstream != Upstream{}) {
			t.Fatalf("tried to retrieve connection from pool, got prompted to make a connection instead."+
				" size: [%d], pool: [%v], upstream: [%v] entry: [%d]", pool.Size(), pool, upstream, i)
		}

		upstreamNamesSeen[UpstreamName(ce.GetAddress())] = true
		// add the connection back to the pool in order to weight down the upstream and get the next one
		ce.AddExchange(time.Duration(i+1) * time.Minute)
		if err := pool.Add(ce); err != nil {
			t.Fatalf("error inserting entry [%v]: %s", ce, err)
		}
	}
}

func TestIllegalUpstreamAddition(t *testing.T) {
	pool := buildPool()
	ce := &connEntry{
		upstream: Upstream{
			Name: "doesntexist",
		},
	}

	if err := pool.Add(ce); err == nil {
		t.Fatalf("was able to add conn entry [%v] with non-existent upstream [%v] to pool [%v]", ce, ce.upstream, pool)
	}
}

func TestConnectionPoolSize(t *testing.T) {
	pool := buildPool()
	max := 10
	f := func(idx int) ConnEntry {
		upstream := Upstream{Name: "example.com", Port: idx}
		return &connEntry{upstream: upstream}
	}
	// we want to add distinct entries in different sub-pools, based on the current
	// implementation which stores connections separately for each host
	for i := 0; i < max; i++ {
		ce := f(i)
		pool.AddUpstream(ce.GetUpstream())
		for j := 0; j < max; j++ {
			if err := pool.Add(ce); err != nil {
				t.Fatalf("tried to add connection [%v] to pool [%v], got potential error [%s]", ce, pool, err)
			}
		}
	}

	if pool.Size() != max*max {
		t.Fatalf("got the wrong size for the pool, expected [%d], got [%d]", max*max, pool.Size())
	}
}

func TestConnectionPoolWeighting(t *testing.T) {
	pool := NewConnPool()
	upstream, upstream1 := &Upstream{Name: "example.com"}, &Upstream{Name: "test.example.com"}
	pool.AddUpstream(upstream)
	pool.AddUpstream(upstream1)

	ce, err := pool.NewConnection(*upstream, UpstreamTestingDialer(*upstream))
	if err != nil {
		t.Fatalf("could not make connection with upstream [%v]: %s", upstream, err)
	}
	weight := ce.GetWeight()

	if weight != 0.0 {
		t.Fatalf("weight on new connection was wrong: [%f] != 0", weight)
	}

	// add an exchange so that the other upstream gets preferred
	ce.AddExchange(time.Duration(1) * time.Millisecond)
	if err := pool.Add(ce); err != nil {
		t.Fatalf("got error trying to add ce [%v] to pool [%v]: %s", ce, pool, err.Error())
	}

	// Once a connection is in the pool, does it get returned or does the pool prompt for additional connections?
	// this will demonstrate that even in the presence of upstreams with connections, the pool will attempt
	// to make new ones for upstreams that haven't seen any action yet
	ce2, upstream2 := pool.Get()
	if (upstream2 == Upstream{}) {
		t.Fatalf("expected to receive prompt to connect to new upstream, got cached conn [%v] instead", ce2)
	}

	// weight this so that the other upstream will bubble up
	ce.AddExchange(time.Duration(2) * time.Millisecond)
	if err := pool.Add(ce); err != nil {
		t.Fatalf("got error trying to add ce [%v] to pool [%v]: %s", ce2, pool, err.Error())
	}

	ce, u := pool.Get()
	if (u == Upstream{}) {
		t.Fatalf("expected to receive connection prompt, got connection [%v] instead", ce)
	}

	// just in case, make sure this is the right upstream
	if u.GetAddress() != upstream1.GetAddress() {
		// nolint:lll
		t.Fatalf("conn pool prompted to connect to the wrong upstream! %s([%v]) != %s([%v])", u.GetAddress(), u, upstream.GetAddress(), upstream)
	}
	ce, err = pool.NewConnection(u, UpstreamTestingDialer(u))
	if err != nil {
		t.Fatalf("could not make connection with upstream [%v]: %s", u, err)
	}

	// weight it heavily so that the original connection will come up
	ce.AddExchange(time.Duration(5) * time.Millisecond)
	if err := pool.Add(ce); err != nil {
		t.Fatalf("couldn't re-add connentry [%v] to pool: %s", ce, err)
	}

	// now the original connection should be there
	ce, u = pool.Get()
	if (u != Upstream{}) {
		t.Fatalf("expected to receive connection, got prompted to connect to [%v] instead", u)
	}

	if ce.GetAddress() != upstream.GetAddress() {
		t.Fatalf("expected to retrieve connection to [%s], got ce to [%s]: [%v]", upstream.GetAddress(), ce.GetAddress(), ce)
	}
}

func TestConnectionPoolAddSlowConnection(t *testing.T) {
	pool := buildPool()
	upstream, upstream1 := &Upstream{Name: "example.com"}, &Upstream{Name: "test.example.com"}
	upstream.SetWeight(1)
	pool.AddUpstream(upstream)
	pool.AddUpstream(upstream1)

	ce, err := pool.NewConnection(*upstream1, func(addr string) (*dns.Conn, error) {
		server, client := net.Pipe()
		server.Close()
		return &dns.Conn{Conn: client}, nil
	})

	if err != nil {
		t.Fatalf("could not make connection with upstream [%v]: %s", upstream, err)
	}

	ce.AddExchange(time.Duration(1) * time.Hour)
	if err := pool.Add(ce); err != nil {
		t.Fatalf("got error trying to add ce [%v] to pool [%v]: %s", ce, pool, err.Error())
	}

	ce2, u := pool.Get()
	if u.Name != upstream.Name {
		t.Fatalf("expected slow connection to have been closed! [%v] [%v]", u, ce2)
	}
}

/** BENCHMARKS **/

func BenchmarkConnectionParallel(b *testing.B) {
	pool := NewConnPool()
	b.RunParallel(func(pb *testing.PB) {
		i := 1
		for pb.Next() {
			upstream := Upstream{
				Port: i,
				Name: "example.com",
			}
			ce, err := pool.NewConnection(upstream, UpstreamTestingDialer(upstream))
			if err != nil {
				b.Fatalf("could not create new connection for upstream %d: %s", i, err)
			} else {
				pool.CloseConnection(ce)
			}
			i++
		}
	})
}

/** This seems to brick the benchmark framework, it keeps retrying until so many
    tests are in motion that the box freezes.  I don't think it's worth the pain
    of maintaining it, so i'm leaving it commented out.
func BenchmarkConnectionSerial(b *testing.B) {
	server, _, err := buildTestResources()
	if err != nil {
		b.Fatalf("could not initialize server [%s]", err)
	}
	pool := server.GetConnectionPool()
	for i := 0; i < b.N; i++ {
		upstream := Upstream{
			Name: "example.com",
			Port: i,
		}
		pool.NewConnection(upstream, UpstreamTestingDialer(upstream))
	}
}
**/
