package main

import (
  "time"
  "log"
  "fmt"
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
  // TODO cache this?
  config, err := dns.ClientConfigFromFile("/etc/resolv.conf")
  if err != nil {
    return []Record{}, fmt.Errorf("error parsing resolv.conf: %s\n", err)
  }

  c := new(dns.Client)
  m := new(dns.Msg)
  m.SetQuestion(domain, rrtype)
  m.RecursionDesired = true
  r, _, err := c.Exchange(m, config.Servers[0]+":"+config.Port)
  if err != nil {
    return []Record{}, fmt.Errorf("error looking up domain [%s]: %s",domain, err)
  }

  /**
  if r.Rcode != dns.RcodeSuccess {
    return []Record{}, fmt.Errorf("got unsuccessful lookup code [%d]", r.Rcode)
  }
  **/
  for _, a := range r.Answer {
    r := Record {
      Entry: a,
      Qtype: rrtype,
      CreationTime: time.Now(),
      Ttl: (time.Duration(a.Header().Ttl) * time.Second),
    }
    log.Printf("created %v\n", r)
    records = append(records, r)
  }
  return records, nil
}

// retrieves the record for that domain, either from cache or from
// a recursive query
func (server *Server) RetrieveRecords(domain string, rrtype uint16) ([]*Record, error) {
  var records []*Record

  // First: check cache
  log.Printf("retrieving %s from cache", domain)
  server.Cache.RLock()
  cached_records, ok := server.Cache.Get(domain, rrtype)
  defer server.Cache.RUnlock()
  if ok {
    return cached_records, nil
  }

  // Second, query upstream if there's no cache
  log.Printf("falling back to query")
  qrec, err := server.RecursiveQuery(domain, rrtype)
  if err != nil {
    return records, fmt.Errorf("error running recursive query on domain [%s]: %s\n", domain, err)
  }
  for _, record := range qrec {
    log.Printf("adding %v\n", record)
    server.Cache.Lock()
    server.Cache.Add(&record)
    server.Cache.Unlock()
    log.Printf("done adding %v\n", record)
    records = append(records, &record)
  }
  return records, nil
}

func (server *Server) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
  msg := dns.Msg{}
  msg.SetReply(r)
  domain := msg.Question[0].Name
  // FIXME what does this change?
  msg.Authoritative = true

  records, err := server.RetrieveRecords(domain, r.Question[0].Qtype)
  log.Printf("records: [%v]\n", records)
  if err != nil {
    log.Printf("error retrieving record for domain [%s]: %s", domain, err)
    // TODO SERVFAIL here
    sendNXDomain(w, r)
    return
  }
  for _, record := range records {
    log.Printf("each: %v\n", record)
    msg.Answer = append(msg.Answer, record.Entry.(dns.RR))
  }

  w.WriteMsg(&msg)
}

func NewServer() (*Server, error) {
  ret := &Server{}
  newcache, err := NewCache()
  if err != nil {
    return nil, fmt.Errorf("couldn't initialize cache: %s\n", err) 
  }
  ret.Cache = newcache
  return ret, nil
}
