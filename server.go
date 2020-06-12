package main

// Generic functions and types for servers.
import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"regexp"
	"syscall"
	"time"

	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sys/unix"
)

// making this to support dependency injection into the server.
type Client interface {
	// Make a new connection
	Dial(address string) (conn *dns.Conn, err error)

	// Run DNS queries
	ExchangeWithConn(s *dns.Msg, conn *dns.Conn) (r *dns.Msg, rtt time.Duration, err error)
}

// this abstraction helps us test the entire servedns path.
type ResponseWriter interface {
	WriteMsg(*dns.Msg) error
}

type Server interface {
	// Needs to handle DNS queries
	dns.Handler

	// Internal function to implement ServeDNS, this allows testing
	HandleDNS(w ResponseWriter, m *dns.Msg)

	// Retrieves a new connection to an upstream
	GetConnection() (ConnEntry, error)

	// Runs a recursive query for a given record and record type
	RecursiveQuery(domain string, rrtype uint16) (Response, string, error)

	// Retrieves records from cache or an upstream
	RetrieveRecords(domain string, rrtype uint16) (Response, string, error)

	// Retrieve the cache of locally hosted records
	GetHostedCache() Cache

	// Add a upstream to the server's list
	AddUpstream(u *Upstream)

	// Get a copy of the connection pool for this server
	GetConnectionPool() ConnPool
}

func processResults(r dns.Msg, domain string, rrtype uint16) Response {
	ttl := uint32(0)
	if len(r.Answer) > 0 {
		ttl = r.Answer[0].Header().Ttl
	}

	return Response{
		Ttl:          time.Duration(ttl) * time.Second,
		Entry:        r,
		CreationTime: time.Now(),
		Name:         domain,
		Qtype:        rrtype,
	}
}

func sendServfail(w ResponseWriter, duration time.Duration, r *dns.Msg) {
	LocalServfailsCounter.Inc()
	m := &dns.Msg{}
	m.SetRcode(r, dns.RcodeServerFailure)
	if err := w.WriteMsg(m); err != nil {
		Logger.Log(LogMessage{
			Level: ERROR,
			Context: LogContext{
				"what":  "error writing servfail reply",
				"error": err.Error(),
			},
		})
	}
	logQuery("servfail", duration, m)
}

func logQuery(source string, duration time.Duration, response *dns.Msg) {
	var queryContext LogContext
	// if we output in JSON, the tab character is lost, but queries rely on it to be readable
	// so we manually sub the tabs for something better
	r := regexp.MustCompile("\t+")
	for _, q := range response.Question {
		for _, a := range response.Answer {
			queryContext = LogContext{
				"ID":           fmt.Sprintf("%d", response.Id),
				"question":     string(r.ReplaceAll([]byte(q.String()), []byte("    "))),
				"answer":       string(r.ReplaceAll([]byte(a.String()), []byte("    "))),
				"answerSource": source,
				"duration":     duration.String(),
			}
			QueryLogger.Log(LogMessage{
				Context: queryContext,
			})
		}
	}
}

func sockoptSetter(network, address string, c syscall.RawConn) (err error) {
	config := GetConfiguration()
	err = c.Control(func(fd uintptr) {
		if config.UseTfo {
			if err := unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_FASTOPEN_CONNECT, 1); err != nil {
				log.Printf("could not set TCP fast open to [%s]: %s", address, err.Error())
			}
		}
	})
	return
}

func buildDialer(timeout time.Duration) (dialer *net.Dialer) {
	return &net.Dialer{
		Control: sockoptSetter,
		Timeout: timeout,
	}
}

func BuildClient() *dns.Client {
	config := GetConfiguration()
	timeout := config.Timeout * time.Millisecond
	cl := &dns.Client{
		SingleInflight: true,
		Dialer:         buildDialer(timeout),
		Timeout:        timeout,
		Net:            "tcp-tls",
		TLSConfig: &tls.Config{
			// nolint:gosec // this is for intentionally decreasing security during testing
			InsecureSkipVerify: config.SkipUpstreamVerification,
		},
	}
	Logger.Log(LogMessage{
		Level: INFO,
		Context: LogContext{
			"what": "instantiated new dns client in TLS mode",
			"next": "returning for use",
		},
	})
	return cl
}

// assumes that the caller will close connection upon any errors.
func attemptExchange(m *dns.Msg, ce ConnEntry, client Client) (reply *dns.Msg, err error) {
	address := ce.GetAddress()
	exchangeTimer := prometheus.NewTimer(prometheus.ObserverFunc(func(v float64) {
		ExchangeTimer.WithLabelValues(address).Observe(v)
	}),
	)
	reply, rtt, err := client.ExchangeWithConn(m, ce.GetConn())
	exchangeTimer.ObserveDuration()
	ce.AddExchange(rtt)
	if err != nil {
		UpstreamErrorsCounter.WithLabelValues(address).Inc()
		Logger.Log(NewLogMessage(
			ERROR,
			LogContext{
				"what":  fmt.Sprintf("error looking up domain [%s] on server [%s]", m.Question[0].Name, address),
				"error": fmt.Sprintf("%s", err),
			},
			func() string { return fmt.Sprintf("request [%v]", m) },
		))
		// try the next one
		return &dns.Msg{}, err
	}
	return reply, nil
}
