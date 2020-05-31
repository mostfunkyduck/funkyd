package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/miekg/dns"
)

var (
	confFile    = flag.String("conf", "", "location of the funkyd configuration file")
	versionFlag = flag.Bool("version", false, "output version")
)

func validateConfFile() error {
	file := *confFile

	if file == "" {
		return fmt.Errorf("no configuration file specified")
	}

	if _, err := os.Stat(file); err != nil {
		return err
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

var servers []*dns.Server = []*dns.Server{}

func addServer(s *dns.Server) {
	servers = append(servers, s)
}

var shutdownMutex *sync.Mutex = &sync.Mutex{}

func Shutdown() {
	shutdownMutex.Lock()
	defer shutdownMutex.Unlock()
	for _, s := range servers {
		ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
		defer cancel()
		if err := s.ShutdownContext(ctx); err != nil {
			log.Printf("error shutting down server [%v] : %s", s, err)
		}
	}
}

func main() {
	flag.Parse()

	if *versionFlag {
		fmt.Printf("current version: %s\n", GetVersion().String())
		os.Exit(0)
	}

	if err := validateFlags(); err != nil {
		fmt.Printf("error validating flags: %s\n", err.Error())
		os.Exit(1)
	}
	rand.Seed(time.Now().UnixNano())
	BuildInfoGauge.WithLabelValues(GetVersion().String()).Set(1)
	log.Printf("reading configuration from [%s]", *confFile)
	// read in configuration
	if err := InitConfiguration(*confFile); err != nil {
		log.Fatalf("could not open configuration: %s\n", err)
	}

	config := GetConfiguration()

	InitLoggers()
	InitApi()

	server, err := NewMutexServer(nil, nil)
	if err != nil {
		Logger.Log(LogMessage{
			Level: CRITICAL,
			Context: LogContext{
				"what":  "could not build mutex server",
				"error": err.Error(),
			},
		})
		os.Exit(1)
	}

	loadLocalZones(server)

	dnsPort := config.DnsPort
	if dnsPort == 0 {
		dnsPort = 53
	}

	// set up DNS server
	srvUDP := &dns.Server{Addr: ":" + strconv.Itoa(dnsPort), Net: "udp", MaxTCPQueries: -1, ReusePort: true}
	srvTCP := &dns.Server{Addr: ":" + strconv.Itoa(dnsPort), Net: "tcp", MaxTCPQueries: -1, ReusePort: true}

	srvUDP.Handler, srvTCP.Handler = server, server

	addServer(srvUDP)
	addServer(srvTCP)

	if config.Blackhole {
		// PSYCH!
		if err := runBlackholeServer(); err != nil {
			Logger.Log(LogMessage{
				Level: CRITICAL,
				Context: LogContext{
					"what":  "failed to start blackhole server",
					"error": err.Error(),
				},
			})
			os.Exit(1)
		}
	}

	Logger.Log(LogMessage{
		Level: CRITICAL,
		Context: LogContext{
			"what":    "starting up TCP and UDP servers",
			"version": GetVersion().String(),
			"port":    fmt.Sprintf("%d", dnsPort),
		},
	})

	go func(srvUDP *dns.Server) {
		if err := srvUDP.ListenAndServe(); err != nil {
			Logger.Log(LogMessage{
				Level: CRITICAL,
				Context: LogContext{
					"what":  "error serving UDP",
					"error": err.Error(),
				},
			})
		}
	}(srvUDP)

	if err := srvTCP.ListenAndServe(); err != nil {
		Logger.Log(LogMessage{
			Level: CRITICAL,
			Context: LogContext{
				"what":  "error serving TCP",
				"error": err.Error(),
			},
		})
		// bail here so it doesn't deadlock on the shutdown mutex
		os.Exit(1)
	}

	// wait until all shutdowns are complete
	shutdownMutex.Lock()
}
