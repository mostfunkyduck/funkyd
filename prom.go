package main

import (
  "github.com/gorilla/mux"
  "github.com/prometheus/client_golang/prometheus"
  "github.com/prometheus/client_golang/prometheus/promauto"
  "github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
  TotalDnsQueriesCounter = promauto.NewCounter(prometheus.CounterOpts{
    Name: "ttl_dns_queries",
    Help: "The total number of handled DNS queries",
  })
  CacheSizeGauge = promauto.NewGauge(prometheus.GaugeOpts{
    Name: "ttl_cache_entries",
    Help: "total size of cache",
  })
  CacheHitsCounter = promauto.NewCounter(prometheus.CounterOpts{
    Name: "ttl_cache_hits",
    Help: "The total number of local cache hits",
  })
  HostedCacheHitsCounter = promauto.NewCounter(prometheus.CounterOpts{
    Name: "ttl_hosted_cache_hits",
    Help: "The total number of locally hosted hits",
  })
  RecursiveQueryCounter = promauto.NewCounter(prometheus.CounterOpts{
    Name: "ttl_recursive_queries",
    Help: "The total number of recursive queries run by this server",
  })
  ResolverErrorsCounter = promauto.NewCounter(prometheus.CounterOpts{
    Name: "ttl_resolver_errors",
    Help: "The total number of times an upstream resolver had errors",
  })
  LocalServfailsCounter = promauto.NewCounter(prometheus.CounterOpts{
    Name: "ttl_local_servfails",
    Help: "The total number of times the local server had to throw SERVFAIL",
  })
)

func InitPrometheus(router *mux.Router) {
  router.Handle("/metrics", promhttp.Handler())
}
