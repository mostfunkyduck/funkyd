package main

// This module handles the local zone file management.
import (
	"fmt"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// lifted and modified from cli53 code for this
// parses out a slice of Records from the contents of a zone file
// zone: the full text of a bind file
func ParseZoneFile(zone string) ([]Response, error) {
	z := dns.NewZoneParser(strings.NewReader(zone), ".", "")
	responses := make([]Response, 0)

	for rr, done := z.Next(); done; rr, done = z.Next() {
		if err := z.Err(); err != nil {
			return nil, fmt.Errorf("token error: %s", err)
		}

		// TODO the stuff we host sholdn't follow the same rules, check out the spec
		cachedMsg := dns.Msg{
			Answer: []dns.RR{rr},
		}
		response := Response{
			Name:         rr.Header().Name,
			Entry:        cachedMsg,
			Ttl:          time.Duration(rr.Header().Ttl) * time.Second,
			CreationTime: time.Now(),
			Qtype:        rr.Header().Rrtype,
		}
		responses = append(responses, response)
	}

	return responses, nil
}
