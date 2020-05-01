package main

import (
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	TotalDnsQueriesCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "stubbage_dns_queries_total",
		Help: "The total number of handled DNS queries",
	})
	CacheSizeGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "stubbage_cache_entries_total",
		Help: "total size of cache",
	})
	CacheHitsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "stubbage_cache_hits_total",
		Help: "The total number of local cache hits",
	})
	HostedCacheHitsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "stubbage_hosted_cache_hits_total",
		Help: "The total number of locally hosted hits",
	})
	RecursiveQueryCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "stubbage_recursive_queries_total",
		Help: "The total number of recursive queries run by this server",
	})
	ResolverErrorsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "stubbage_resolver_errors_total",
		Help: "The total number of times an upstream resolver had errors",
	})
	LocalServfailsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "stubbage_servfails_total",
		Help: "The total number of times the local server had to throw SERVFAIL",
	})
	NXDomainCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "stubbage_nxdomains_total",
		Help: "total nxdomains",
	})
	QueryTimer = promauto.NewSummary(prometheus.SummaryOpts{
		Name:       "stubbage_query_time",
		Help:       "query timer",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	})
	TLSTimer = promauto.NewSummary(prometheus.SummaryOpts{
		Name:       "stubbage_tls_connection_time",
		Help:       "times the pure connection time of tls",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	})
)

func InitPrometheus(router *mux.Router) {
	router.Handle("/metrics", promhttp.Handler())
}
