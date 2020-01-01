package main

import (
  "time"
  "log"
  "fmt"
  "github.com/miekg/dns"
  "crypto/tls"
)

// convenience function to generate an nxdomain response
func sendNXDomain (w dns.ResponseWriter, r *dns.Msg) {
  log.Printf("sending nxdomain")
  m := &dns.Msg{}
  m.SetRcode(r, dns.RcodeNameError)
  w.WriteMsg(m)
}

func (server *Server) processResults(r *dns.Msg, domain string, rrtype uint16) ([]Record, error) {
  var records []Record

  if r.Rcode != dns.RcodeSuccess {
    // wish we had an easier way of translating the rrtype into human, but i don't got one yet
    return []Record{}, fmt.Errorf("got unsuccessful lookup code for domain [%s] rrtype [%d]: [%d]", domain, rrtype, r.Rcode)
  }

  for _, a := range r.Answer {
    r := Record {
      Key: domain,
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

func (server *Server) RecursiveQuery(domain string, rrtype uint16) ([]Record, error) {
  // lifted from example code https://github.com/miekg/dns/blob/master/example_test.go
  // TODO cache this? config this too
  port := "853"
  config, err := dns.ClientConfigFromFile("./resolv.conf") // can be /etc/resolv.conf on a server configured with tcp shit in its config
  if err != nil {
    return []Record{}, fmt.Errorf("error parsing resolv.conf: %s\n", err)
  }

  c := &dns.Client{
    Net: "tcp-tls",
    TLSConfig: &tls.Config{},
  }
  m := &dns.Msg{}
  m.SetQuestion(domain, rrtype)
  m.RecursionDesired = true
  // TODO cycle through all servers. cache connection
  // build a huge error string of all errors from all servers
  var errorstring string
  for _, s := range config.Servers {
    r, _, err := c.Exchange(m, s + ":" + port)
    if err != nil {
      errorstring = fmt.Sprintf("error looking up domain [%s] on server [%s:%s]: %s: %s", domain, s, port, err, errorstring)
      continue
    }
    return server.processResults(r, domain, rrtype)
  }
  // if we went through each server and couldn't find at least one result, bail with the full error string
  return []Record{}, fmt.Errorf(errorstring)
}

// retrieves the record for that domain, either from cache or from
// a recursive query
func (server *Server) RetrieveRecords(domain string, rrtype uint16) ([]*Record, error) {
  var records []*Record

  // First: check caches
  log.Printf("retrieving %s from cache [%v]", domain, server)
  // Need to keep caches locked until the end of the function because 
  // we may need to add records consistently
  server.Cache.Lock()
  defer server.Cache.Unlock()
  cached_records, ok := server.Cache.Get(domain, rrtype)
  if ok {
    return cached_records, nil
  }

  // Now check  the hosted cache
  server.HostedCache.RLock()
  defer server.HostedCache.RUnlock()
  cached_records, ok = server.HostedCache.Get(domain, rrtype)
  if ok {
    return cached_records, nil
  }

  // Second, query upstream if there's no cache
  log.Printf("falling back to query")
  // TODO only do if requested
  qrec, err := server.RecursiveQuery(domain, rrtype)
  if err != nil {
    return records, fmt.Errorf("error running recursive query on domain [%s]: %s\n", domain, err)
  }
  for _, record := range qrec {
    server.Cache.Add(&record)
    records = append(records, &record)
  }
  return records, nil
}

func (server *Server) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
  msg := dns.Msg{}
  msg.SetReply(r)
  domain := msg.Question[0].Name
  // FIXME when should this be set to what
  msg.Authoritative = false
  msg.RecursionAvailable = true

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
    return nil, fmt.Errorf("couldn't initialize lookup cache: %s\n", err) 
  }
  newcache.Init()
  ret.Cache = newcache

  hostedcache, err := NewCache()
  // don't init, we don't clean this one
  ret.HostedCache = hostedcache
  return ret, nil
}
