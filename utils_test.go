package main

import (
	"fmt"
	"github.com/miekg/dns"
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
