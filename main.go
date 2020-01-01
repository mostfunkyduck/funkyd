package main

import (
  "log"
  "io/ioutil"
  "strconv"
  "github.com/miekg/dns"
)

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

  // read in zone files, if configured to do so
  for _,file := range config.ZoneFiles {
    file, err := ioutil.ReadFile(file)
    if err != nil {
      log.Fatalf("could not read zone file [%s]: %s\n", file, err)
    }
    records, err := ParseZoneFile(string(file))
    if err != nil {
      log.Fatalf("could not parse zone file [%s]: %s\n", file, err)
    }
    for _, record := range records {
      log.Printf("adding [%v]\n", record)
      // TODO one function to make the keys, please
      server.HostedCache.Lock()
      server.HostedCache.Add(&record)
      server.HostedCache.Unlock()
    }
  }

  // set up DNS server
  srv := &dns.Server{Addr: ":" + strconv.Itoa(53), Net: "udp"}
  srv.Handler = server
  if err := srv.ListenAndServe(); err != nil {
    log.Fatalf("Failed to set udp listener %s\n", err.Error())
  }
}
