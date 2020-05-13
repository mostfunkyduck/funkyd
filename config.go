package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type logConfig struct {
	// What log level to use
	Level LogLevel `json:"level"`

	// Where the log file should live
	Location string `json:"location"`
}

// For server side tls configuration
type tlsConfig struct {
	// Private key file
	PrivateKeyFile string `json:"private_key_file"`

	// Public cert file
	CertificateFile string `json:"certificate_file"`
}

type Configuration struct {
	// Dial timeout in seconds
	Timeout time.Duration `json:"timeout"`

	// Location of zone files with local dns configuration
	ZoneFiles []string `json:"zone_files"`

	// List of upstreams, overrides resolv.conf
	Upstreams []UpstreamName `json:"upstreams"`

	// Whether or not to blackhole all DNS traffic
	Blackhole bool `json:"blackhole"`

	// Port to listen for DNS traffic on
	DnsPort int `json:"dns_port"`

	// Port to expose admin API on
	HttpPort int `json:"http_port"`

	// Force a maximum number of concurrent queries, 0 value will set this to GOMAXPROCS
	ConcurrentQueries int `json:"concurrent_queries"`

	// Server logging
	ServerLog logConfig `json:"server_log"`

	// Query logging
	QueryLog logConfig `json:"query_log"`

	// Which protocol to listen on
	ListenProtocol string `json:"listen_protocol"`

	// Optional TLS config for using TLS inbound
	TlsConfig tlsConfig `json:"tls"`

	// skips cert verification, only use in testing pls
	SkipUpstreamVerification bool `json:"skip_upstream_verification"`

	// How many times to retry connections to upstream servers
	UpstreamRetries int `json:"upstream_retries"`
}

// this is a pointer so that tests can set variables easily
// it is initialized here for the same reason
var configuration Configuration = Configuration{}

func InitConfiguration(configpath string) error {
	file, _ := os.Open(configpath)
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&configuration)
	if err != nil {
		return fmt.Errorf("error while loading configuration from JSON: %s\n", err)
	}

	configJSON, err := json.MarshalIndent(configuration, "", "    ")
	if err != nil {
		return fmt.Errorf("could not render configuration [%v] as JSON", configuration)
	}
	fmt.Printf("running configuration: %s\n", string(configJSON))
	return nil
}

func GetConfiguration() *Configuration {
	return &configuration
}
