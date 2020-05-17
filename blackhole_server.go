package main

// A handler that acts as a basic echo server for testing

import (
	"github.com/miekg/dns"
)

type BlackholeServer struct {
}

func (s *BlackholeServer) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	// This is a copy of a test utility function in the dns library, designed as a basic echo function
	m := new(dns.Msg)
	m.SetReply(req)

	m.Extra = make([]dns.RR, 1)
	m.Extra[0] = &dns.TXT{Hdr: dns.RR_Header{Name: m.Question[0].Name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 0}, Txt: []string{"Hello world"}}
	w.WriteMsg(m)
}
