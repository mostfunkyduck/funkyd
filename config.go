package main

// Configuration module, parses configuration into a struct which is
// exposed for general use
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
	// how long to cool upstreams down for if they start throwing errors
	// cooling upstreams will only be used if no other options are available
	// the 0-value equates to 500 ms
	CooldownPeriod time.Duration

	// Whether or not to use TCP Fast Open (hint: this is an experimental protocol
	// that slows the hell out of things when the upstream doesn't support it,
	// the first packet is sent with a payload and if the server doesn't support that,
	// it just drops the packet 9_9
	UseTfo bool `json:"use_tfo"`

	// How often to clean the record cache, in ms
	CleanInterval time.Duration `json:"clean_interval"`

	// How many evicted responses from the record cache should be buffered before deletion
	// by default, responses will be evicted immediately, which may cause a lot of contention
	// for the cache lock
	EvictionBatchSize int `json:"eviction_batch_size"`

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
