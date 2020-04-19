package main

import (
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func InitPrometheus(router *mux.Router) {
	router.Handle("/metrics", promhttp.Handler())
}
