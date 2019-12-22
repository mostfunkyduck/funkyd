package main

import (
  "fmt"
  "log"
  "io/ioutil"
  "net"
  "strconv"
  "github.com/miekg/dns"
)

type Server struct{
  ACache *RecordCache
}
func (server *Server) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
  msg := dns.Msg{}
  msg.SetReply(r)
  switch r.Question[0].Qtype {
  case dns.TypeA:
    msg.Authoritative = true
    domain := msg.Question[0].Name
    record, ok := server.ACache.Get(domain)

    if !ok {
      m := new(dns.Msg)
      m.SetRcode(r, dns.RcodeNameError)
      //m.Ns = []dns.RR{soa(n)}
      w.WriteMsg(m)
      break
    }

    msg.Answer = append(msg.Answer, &dns.A{
      Hdr: dns.RR_Header{ Name: record.Label, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: uint32(record.Ttl) },
      A: net.ParseIP(record.Value),
    })
  }
  w.WriteMsg(&msg)
}

type Record struct {
  Label  string
  Rrtype string
  Ttl    int
  Value  string
}

func ParseZoneFile(file string) (*Record, error){
  return &Record {
    Label: "label",
    Rrtype: "A",
    Ttl: 1,
    Value: "value",
  }, nil
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
func main() {
  // read in configuration
  config, err := NewConfiguration("./test.conf")
  if err != nil {
    log.Fatalf("could not open configuration: %s\n", err)
  }

  server, err := NewServer()
  if err != nil {
    log.Fatalf("could not initialize new server: %s\n", err)
  }

  for _,file := range config.ZoneFiles {
    file, err := ioutil.ReadFile(file)
    if err != nil {
      log.Fatalf("could not read zone file [%s]: %s\n", file, err)
    }
    record, err := ParseZoneFile(string(file))
    if err != nil {
      log.Fatalf("could not parse zone file [%s]: %s\n", file, err)
    }
    server.ACache.Add(record.Label, record)
  }
  // read in zone files, if configured to do so
  // set up DNS server
  srv := &dns.Server{Addr: ":" + strconv.Itoa(53), Net: "udp"}
  srv.Handler = server
  if err := srv.ListenAndServe(); err != nil {
    log.Fatalf("Failed to set udp listener %s\n", err.Error())
  }
  // set up HTTP server
}
