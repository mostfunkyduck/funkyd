package main

import (
	"github.com/miekg/dns"
	"io/ioutil"
	"log"
	"strconv"
)

func main() {
	// read in configuration
	err := InitConfiguration("./test.conf")
	if err != nil {
		log.Fatalf("could not open configuration: %s\n", err)
	}
	InitApi()
	config := GetConfiguration()
	server, err := NewServer()
	if err != nil {
		log.Fatalf("could not initialize new server: %s\n", err)
	}

	// read in zone files, if configured to do so
	for _, file := range config.ZoneFiles {
		file, err := ioutil.ReadFile(file)
		if err != nil {
			log.Fatalf("could not read zone file [%s]: %s\n", file, err)
		}
		responses, err := ParseZoneFile(string(file))
		if err != nil {
			log.Fatalf("could not parse zone file [%s]: %s\n", file, err)
		}
		for _, response := range responses {
			log.Printf("adding [%v]\n", response)
			// TODO one function to make the keys, please
			server.HostedCache.Lock()
			server.HostedCache.Add(response)
			server.HostedCache.Unlock()
		}
	}

	// set up DNS server
	srv := &dns.Server{Addr: ":" + strconv.Itoa(config.DnsPort), Net: "udp"}
	srv.Handler = server
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Failed to set udp listener %s\n", err.Error())
	}
}
