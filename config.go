package main

// Configuration module, parses configuration into a struct which is
// exposed for general use.
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

// For server side tls configuration.
type tlsConfig struct {
	// Private key file
	PrivateKeyFile string `json:"private_key_file"`

	// Public cert file
	CertificateFile string `json:"certificate_file"`
}

type Configuration struct {

	// When the average RTT of a connection exceeds this limit, it will be closed
	// and the upstream on the other end will be cooled
	SlowConnectionThreshold int

	// how long to cool upstreams down for if they start throwing errors
	// cooling upstreams will only be used if no other options are available
	// the 0-value equates to 500 ms
	CooldownPeriod time.Duration `json:"cooldown_period"`

	// How often to clean the record cache, in ms
	CleanInterval time.Duration `json:"clean_interval"`

	// Dial timeout in seconds
	Timeout time.Duration `json:"timeout"`

	// How many evicted responses from the record cache should be buffered before deletion
	// by default, responses will be evicted immediately, which may cause a lot of contention
	// for the cache lock
	EvictionBatchSize int `json:"eviction_batch_size"`

	// Port to listen for DNS traffic on
	//nolint
	DnsPort int `json:"dns_port"`

	// Port to expose admin API on
	//nolint
	HttpPort int `json:"http_port"`

	// Force a maximum number of concurrent queries, 0 value will set this to GOMAXPROCS
	ConcurrentQueries int `json:"concurrent_queries"`

	// How many times to retry connections to upstream servers
	UpstreamRetries int `json:"upstream_retries"`

	// Server logging
	ServerLog logConfig `json:"server_log"`

	// Query logging
	QueryLog logConfig `json:"query_log"`

	// Optional TLS config for using TLS inbound
	//nolint
	TlsConfig tlsConfig `json:"tls"`

	// Which protocol to listen on
	ListenProtocol string `json:"listen_protocol"`

	// Location of zone files with local dns configuration
	ZoneFiles []string `json:"zone_files"`

	// List of upstreams, overrides resolv.conf
	Upstreams []UpstreamName `json:"upstreams"`

	// skips cert verification, only use in testing pls
	SkipUpstreamVerification bool `json:"skip_upstream_verification"`

	// Whether or not to use TCP Fast Open (hint: this is an experimental protocol
	// that slows the hell out of things when the upstream doesn't support it,
	// the first packet is sent with a payload and if the server doesn't support that,
	// it just drops the packet 9_9)
	UseTfo bool `json:"use_tfo"`

	// Whether or not to blackhole all DNS traffic
	Blackhole bool `json:"blackhole"`

	// Whether or not to use the pipeline server implementation
	UsePipelineServer bool `json:"use_pipeline_server"`
}

// this is a pointer so that tests can set variables easily
// it is initialized here for the same reason.
//nolint
var configuration Configuration = Configuration{}

func InitConfiguration(configpath string) error {
	file, _ := os.Open(configpath)
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&configuration)
	if err != nil {
		return fmt.Errorf("error while loading configuration from JSON: %s", err)
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
