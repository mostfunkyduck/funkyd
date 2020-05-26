package main

// General utilites
import (
	"fmt"
	"github.com/miekg/dns"
	"net"
	"time"
)

func UpstreamTestingDialer(upstream Upstream) func(addr string) (conn *dns.Conn, err error) {
	expectedAddress := upstream.GetAddress()
	return func(addr string) (conn *dns.Conn, err error) {
		if addr != expectedAddress {
			err = fmt.Errorf("got unexpected address [%s] when dialing upstream [%v], expecting address [%s] to be dialed", addr, upstream, expectedAddress)
		}
		return &dns.Conn{}, err
	}
}

func WaitForCondition(x int, f func() bool) (result bool) {
	for i := 0; i < x; i++ {
		if result = f(); result {
			break
		}
		time.Sleep(time.Duration(100) * time.Millisecond)
	}
	return
}

func buildRequest() (request *dns.Msg) {
	return &dns.Msg{
		Question: []dns.Question{
			dns.Question{
				Name:   "example.com",
				Qtype:  1,
				Qclass: 1,
			},
		},
	}

}
func buildQuery() (q Query) {
	writer := &MockResponseWriter{}
	qdt := &MockQueryDurationTimer{}

	q = Query{
		W:     writer,
		Msg:   &dns.Msg{},
		Reply: &dns.Msg{},
		Timer: qdt,
	}
	return q
}

func buildAnswer() (m *dns.Msg) {
	a, err := dns.NewRR("example.com.	123	IN	A	10.0.0.0")
	if err != nil {
		panic(fmt.Sprintf("couldn't create answer for query: %s", err))
	}
	return &dns.Msg{
		Answer: []dns.RR{
			a,
		},
	}
}

func buildConnEntry() (c *ConnEntry) {
	s, cl := net.Pipe()
	s.Close()
	return &ConnEntry{
		Conn: &dns.Conn{
			Conn: cl,
		},
	}
}
