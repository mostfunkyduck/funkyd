package main

import (
	"fmt"
	"github.com/miekg/dns"
	"net"
	"testing"
	"time"
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
		t.Fatalf("tried to retrieve connection from pool, was prompted to make a new connection instead, upstream was [%v]", upstream)
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
	upstreamNamesSeen := make(map[UpstreamName]bool)
	max := 10
	f := func(i int) UpstreamName {
		return UpstreamName(fmt.Sprintf("entry #%d", i))
	}

	var upstreams = make([]*Upstream, 0)
	for i := 0; i < max; i++ {
		name := f(i)
		newUpstream := &Upstream{
			Name: UpstreamName(name),
		}
		pool.AddUpstream(newUpstream)
		upstreamNamesSeen[name] = false
		upstreams = append(upstreams, newUpstream)
	}

	for _, each := range upstreams {
		address := each.GetAddress()
		ce, err := pool.NewConnection(*each, func(addr string) (*dns.Conn, error) {
			server, client := net.Pipe()
			server.Close()
			return &dns.Conn{Conn: client}, nil
		})
		if err != nil {
			t.Fatalf("could not get new connection on address [%s] upstream [%v]: %s", address, each, err.Error())
		}

		if err := pool.Add(ce); err != nil {
			t.Fatalf("error inserting entry [%v] for upstream [%v] on address [%s]: %s", ce, each, address, err.Error())
		}
	}

	for i := 0; i < max; i++ {
		ce, upstream := pool.Get()

		if (upstream != Upstream{}) {
			t.Fatalf("tried to retrieve connection from pool, got prompted to make a connection instead. size: [%d], pool: [%v], upstream: [%v] entry: [%d]", pool.Size(), pool, upstream, i)
		}

		upstreamNamesSeen[UpstreamName(ce.GetAddress())] = true
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
		t.Fatalf("was able to add conn entry [%v] with non-existant upstream [%v] to pool [%v]", ce, ce.upstream, pool)
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
	pool := buildPool()
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

	if err := pool.Add(ce); err != nil {
		t.Fatalf("got error trying to add ce [%v] to pool [%v]: %s", ce, pool, err.Error())
	}

	// Once a connection is in the pool, does it get returned or does the pool prompt for additional connections?
	// this also tests that lower weighted resolvers don't take precedence over ones with connections
	ce2, upstream2 := pool.Get()
	if (upstream2 != Upstream{}) {
		t.Fatalf("expected to receive cached connection, got prompted to connect to [%v] instead", upstream2)
	}

	if ce2Addr, upstreamAddr := ce2.GetAddress(), upstream.GetAddress(); ce2Addr != upstreamAddr {
		t.Fatalf("got connection to different upstream [%s] when a connection to [%s] was expected", ce2Addr, upstreamAddr)
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
