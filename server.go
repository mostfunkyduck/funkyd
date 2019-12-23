package main

import (
  "log"
  "fmt"
  "net"
  "github.com/miekg/dns"
)

// convenience function to generate an nxdomain response
func sendNXDomain (w dns.ResponseWriter, r *dns.Msg) {
  m := new(dns.Msg)
  m.SetRcode(r, dns.RcodeNameError)
  //TODO m.Ns = []dns.RR{soa(n)}
  w.WriteMsg(m)
}

func (server *Server) RecursiveQuery(domain string, rrtype uint16) ([]Record, error) {
  var records []Record
  // lifted from example code https://github.com/miekg/dns/blob/master/example_test.go
  config, _ := dns.ClientConfigFromFile("/etc/resolv.conf")
  c := new(dns.Client)
  m := new(dns.Msg)
  m.SetQuestion(domain, rrtype)
  m.RecursionDesired = true
  r, _, err := c.Exchange(m, config.Servers[0]+":"+config.Port)
  if err != nil {
    return []Record{}, fmt.Errorf("error looking up domain [%s]: %s",domain, err)
  }

  if r.Rcode != dns.RcodeSuccess {
    return []Record{}, fmt.Errorf("got unsuccessful lookup code [%d]", r.Rcode)
  }
  for _, a := range r.Answer {
    val := a.(*dns.A).A.String()
    log.Printf("a record: %s\n", val)
    r := Record {
      Label: domain,
      Rrtype: rrtype,
      Ttl: int(a.(*dns.A).Header().Ttl),
      Value: string(val),
    }
    records = append(records, r)
  }
  return records, nil
}

// retrieves the record for that domain, either from cache or from
// a recursive query
func (server *Server) RetrieveRecords(domain string) ([]*Record, error) {
  var records []*Record
  // FIXME cache should return ALL records, not just one
  record, ok := server.ACache.Get(domain)

  if !ok {
    temp, err := server.RecursiveQuery(domain, dns.TypeA)
    if err != nil {
      return records, fmt.Errorf("error running recursive query on domain [%s]: %s\n", domain, err)
    }

    for _, record := range temp {
      records = append(records, &record)
    }
    return records, nil
  } else {
    records = append(records, record)
  }
  return records, nil 
}

func (server *Server) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
  msg := dns.Msg{}
  msg.SetReply(r)
  switch r.Question[0].Qtype {
  case dns.TypeA:
    msg.Authoritative = true
    domain := msg.Question[0].Name
    records, err := server.RetrieveRecords(domain)
    log.Printf("records: [%v]\n", records)
    if err != nil {
      log.Printf("error retrieving record for domain [%s]: %s", domain, err)
      // TODO SERVFAIL here
      sendNXDomain(w, r)
      return
    }
    for _, record := range records {
      log.Printf("each: %v\n", record)
      msg.Answer = append(msg.Answer, &dns.A{
        Hdr: dns.RR_Header{
          Name: record.Label,
          Rrtype: record.Rrtype,
          Class: dns.ClassINET,
          Ttl: uint32(record.Ttl),
        },
        A: net.ParseIP(record.Value),
      })
    }
  }
  w.WriteMsg(&msg)
}

func NewServer() (*Server, error) {
  ret := &Server{}
  newcache, err := NewCache()
  if err != nil {
    return nil, fmt.Errorf("couldn't initialize cache: %s\n", err) 
  }
  ret.ACache = newcache
  return ret, nil
}
