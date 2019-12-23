package main

import (
  "strings"
  "fmt"
  "log"
  "github.com/miekg/dns"
)

// lifted and modified from cli53 code for this
// parses out a slice of Records from the contents of a zone file
// zone: the full text of a bind file
func ParseZoneFile(zone string) ([]Record, error) {
  tokensch := dns.ParseZone(strings.NewReader(zone), ".", "")
  records := make([]Record, 0)

  for token := range tokensch {
    if token.Error != nil {
      return nil, fmt.Errorf("token error: %s\n", token.Error)
    }
    switch rec := token.RR.(type) {
    case *dns.A:
      log.Printf("%v\n", rec)
      record := Record{
        Label:   rec.Header().Name,
        Rrtype:  rec.Header().Rrtype,
        Ttl:     int(rec.Header().Ttl),
        Value:   rec.A.String(),
      }
      records = append(records, record)
    }
  }

  return records, nil
}

