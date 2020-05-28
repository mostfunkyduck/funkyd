package main

// Central place for all prometheus metrics

import (
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	BuildInfoGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "funkyd_build_info",
		Help: "information about the current build",
	},
		[]string{"version"},
	)
	EvictionBufferGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "funkyd_eviction_buffer_gauge",
		Help: "how many records are queued up for deletion",
	})
	UpstreamWeightGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "funkyd_upstream_weight_gauge",
		Help: "the weights of the upstreams in the system, only meaningful when broken down per address",
	},
		[]string{
			// the upstream address
			"address",
			// if there was cooling, was there an error?
			"error",
			// if the upstream is cooling, this will have the time left
			// in its cooldown period
			"cooling"},
	)
	ConnPoolSizeGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "funkyd_conn_pool_size",
		Help: "the total size of the connection pool, labelled by destination host",
	},
		[]string{"destination"},
	)
	QueuedQueriesGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "funkyd_queued_queries_total",
		Help: "dns queries that have been received, but are waiting on a free worker",
	})
	FailedConnectionsCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "funkyd_failed_connections_counter",
		Help: "attempts to connect to an upstream that failed",
	},
		[]string{"destination"},
	)
	TotalDnsQueriesCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "funkyd_dns_queries_total",
		Help: "The total number of handled DNS queries",
	})
	CacheSizeGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "funkyd_cache_entries_total",
		Help: "total size of cache",
	})
	CacheHitsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "funkyd_cache_hits_total",
		Help: "The total number of local cache hits",
	})
	HostedCacheHitsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "funkyd_hosted_cache_hits_total",
		Help: "The total number of locally hosted hits",
	})
	RecursiveQueryCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "funkyd_recursive_queries_total",
		Help: "The total number of recursive queries run by this server",
	})
	UpstreamErrorsCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "funkyd_upstream_errors_total",
		Help: "The total number of times an upstream upstream had errors. This INCLUDES connection closure",
	},
		[]string{"destination"},
	)
	LocalServfailsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "funkyd_servfails_total",
		Help: "The total number of times the local server had to throw SERVFAIL",
	})
	NXDomainCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "funkyd_nxdomains_total",
		Help: "total nxdomains",
	})
	QueryTimer = promauto.NewSummary(prometheus.SummaryOpts{
		Name:       "funkyd_query_time",
		Help:       "query timer",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	})
	ExchangeTimer = promauto.NewSummaryVec(prometheus.SummaryOpts{
		Name:       "funkyd_upstream_exchange_time",
		Help:       "how long the upstreams took to respond",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	},
		[]string{"destination"},
	)
	TLSTimer = promauto.NewSummaryVec(prometheus.SummaryOpts{
		Name:       "funkyd_tls_connection_time",
		Help:       "times the pure connection time of tls",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	},
		[]string{"destination"},
	)
	NewConnectionAttemptsCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "funkyd_new_connection_attempts_total",
		Help: "amount of total new connections attempted",
	},
		[]string{"destination"},
	)
	ReusedConnectionsCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "funkyd_reused_connections_total",
		Help: "times that a connection from the server connection pool was reused",
	},
		[]string{"destination"},
	)
)

func InitPrometheus(router *mux.Router) {
	router.Handle("/metrics", promhttp.Handler())
}
