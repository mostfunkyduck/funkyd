package main

import (
  "time"
  "strings"
  "fmt"
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
    record := Record{
      Key: token.RR.Header().Name,
      Entry: token.RR,
      Ttl: 5,
      CreationTime: time.Now(),
      Qtype: token.RR.Header().Rrtype,
    }
    records = append(records, record)
  }

  return records, nil
}

