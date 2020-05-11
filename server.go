package main

import (
	"crypto/tls"
	"fmt"
	"github.com/miekg/dns"
	"strings"
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
			QueryLogger.Log(NewLogMessage(
				CRITICAL,
				queryContext,
				nil,
			))
		}
	}
	return nil
}

func BuildClient() (*dns.Client, error) {
	config := GetConfiguration()
	cl := &dns.Client{
		SingleInflight: true,
		Timeout:        config.Timeout * time.Millisecond,
		Net:            "tcp-tls",
		TLSConfig: &tls.Config{
			InsecureSkipVerify: config.SkipUpstreamVerification,
		},
	}
	Logger.Log(NewLogMessage(
		INFO,
		LogContext{
			"what": "instantiated new dns client in TLS mode",
			"next": "returning for use",
		},
		nil,
	))
	return cl, nil
}
