package main

// PipelineQueryHandler is the dns.Handler for the pipeline server.

import (
	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
)

type PipelineQueryHandler struct {
	pipelineServerWorker
}

// This implements dns.Handler.
func (p *PipelineQueryHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	p.HandleDNS(w, r)
}

// this is the implementation of ServeDNS that uses a mockable interface instead of dns.ResponseWriter.
func (p *PipelineQueryHandler) HandleDNS(w ResponseWriter, r *dns.Msg) {
	TotalDNSQueriesCounter.Inc()
	queryTimer := prometheus.NewTimer(QueryTimer)

	QueuedQueriesGauge.Inc()
	// dispatch to PipelineCacher
	p.Dispatch(Query{
		Msg:   r,
		W:     w,
		Timer: queryTimer,
	})
	QueuedQueriesGauge.Dec()
}
