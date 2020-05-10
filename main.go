package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"github.com/miekg/dns"
	"io/ioutil"
	"log"
	"os"
	"strconv"
)

var (
	confFile   = flag.String("conf", "", "location of the funkyd configuration file")
	cpuprofile = flag.String("cpuprofile", "", "write cpu profile to file")
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

func runBlackholeServer() error {
	config := GetConfiguration()
	switch config.ListenProtocol {
	case "tcp-tls":
		srv := &dns.Server{Addr: ":" + strconv.Itoa(config.DnsPort), Net: "tcp-tls", MaxTCPQueries: -1, ReusePort: true}
		log.Printf("starting tls blackhole server")
		if (config.TlsConfig == tlsConfig{}) {
			log.Fatalf("attempted to listen for TLS connections, but no tls config was defined")
		}
		if config.TlsConfig.CertificateFile == "" {
			log.Fatalf("invalid certificate file in configuration")
		}

		if config.TlsConfig.PrivateKeyFile == "" {
			log.Fatalf("invalid private key in configuration")
		}

		cert, err := tls.LoadX509KeyPair(config.TlsConfig.CertificateFile, config.TlsConfig.PrivateKeyFile)
		if err != nil {
			log.Fatalf("could not load tls files")
		}

		srv.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
		}
		srv.Handler = &BlackholeServer{}
		return srv.ListenAndServe()
	default:
		return fmt.Errorf("unsupported protocol [%s]", config.ListenProtocol)
	}
}

func loadLocalZones(server Server) {
	config := GetConfiguration()
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
			server.GetHostedCache().Add(response)
		}
	}
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

	config := GetConfiguration()

	InitLoggers()
	InitApi()

	server, err := NewMutexServer(nil)
	if err != nil {
		log.Fatalf("could not initialize new server: %s\n", err)
	}

	loadLocalZones(server)

	dnsPort := config.DnsPort
	if dnsPort == 0 {
		dnsPort = 53
	}

	// set up DNS server
	srv := &dns.Server{Addr: ":" + strconv.Itoa(dnsPort), Net: "udp", MaxTCPQueries: -1, ReusePort: true}
	srv2 := &dns.Server{Addr: ":" + strconv.Itoa(dnsPort), Net: "tcp", MaxTCPQueries: -1, ReusePort: true}

	srv.Handler, srv2.Handler = server, server

	if config.Blackhole {
		// PSYCH!
		err := runBlackholeServer()
		if err != nil {
			log.Fatalf("Failed to run blackhole server: %s", err)
		}
	}

	go func(srv2 *dns.Server) {
		if err := srv2.ListenAndServe(); err != nil {
			log.Fatalf("Failed to set up TCP listener: %s", err)
		}
	}(srv2)

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Failed to set up UDP listener %s\n", err.Error())
	}
}
