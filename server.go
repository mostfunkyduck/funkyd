package main

import (
	"crypto/tls"
	"fmt"
	"github.com/miekg/dns"
	"golang.org/x/sys/unix"
	"log"
	"net"
	"strings"
	"syscall"
	"time"
)

func processResults(r dns.Msg, domain string, rrtype uint16) (Response, error) {
	return Response{
		Entry:        r,
		CreationTime: time.Now(),
		Key:          domain,
		Qtype:        rrtype,
	}, nil
}

func sendServfail(w ResponseWriter, duration time.Duration, r *dns.Msg) {
	LocalServfailsCounter.Inc()
	m := &dns.Msg{}
	m.SetRcode(r, dns.RcodeServerFailure)
	w.WriteMsg(m)
	logQuery("servfail", duration, m)
}

func logQuery(source string, duration time.Duration, response *dns.Msg) error {
	var queryContext LogContext
	for i, _ := range response.Question {
		for j, _ := range response.Answer {
			answerBits := strings.Split(response.Answer[j].String(), " ")
			queryContext = LogContext{
				"name":         response.Question[i].Name,
				"type":         dns.Type(response.Question[i].Qtype).String(),
				"opcode":       dns.OpcodeToString[response.Opcode],
				"answer":       answerBits[len(answerBits)-1],
				"answerSource": fmt.Sprintf("[%s]", source),
				"duration":     fmt.Sprintf("%s", duration),
			}
			QueryLogger.Log(LogMessage{
				Context: queryContext,
			})
		}
	}
	return nil
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

func buildDialer() (dialer *net.Dialer) {
	return &net.Dialer{
		Control: sockoptSetter,
	}
}
func BuildClient() (*dns.Client, error) {
	config := GetConfiguration()
	cl := &dns.Client{
		SingleInflight: true,
		Dialer:         buildDialer(),
		Timeout:        config.Timeout * time.Millisecond,
		Net:            "tcp-tls",
		TLSConfig: &tls.Config{
			InsecureSkipVerify: config.SkipUpstreamVerification,
		},
	}
	Logger.Log(LogMessage{
		Level: CRITICAL,
		Context: LogContext{
			"what": "instantiated new dns client in TLS mode",
			"next": "returning for use",
		},
	})
	return cl, nil
}
