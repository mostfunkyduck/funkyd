package main

import (
	"flag"
	"fmt"
	"github.com/mostfunkyduck/dns"
	"io/ioutil"
	"log"
	"os"
	"strconv"
)

var (
	confFile = flag.String("conf", "", "location of the funkyd configuration file")
)

func validateConfFile() error {
	file := *confFile
	if _, err := os.Stat(file); err != nil {
		return err
	}

	if file == "" {
		return fmt.Errorf("no configuration file specified")
	}

	return nil
}

func validateFlags() error {
	if err := validateConfFile(); err != nil {
		return err
	}
	return nil
}

func main() {
	flag.Parse()
	validateFlags()

	log.Printf("reading configuration from [%s]", *confFile)
	// read in configuration
	err := InitConfiguration(*confFile)
	if err != nil {
		log.Fatalf("could not open configuration: %s\n", err)
	}
	InitLoggers()
	InitApi()
	server, err := NewServer()
	if err != nil {
		log.Fatalf("could not initialize new server: %s\n", err)
	}

	// read in zone files, if configured to do so
	config := GetConfiguration()
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
