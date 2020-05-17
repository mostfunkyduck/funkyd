package main

// HTTP admin API

import (
	"encoding/json"
	"fmt"
	"github.com/gorilla/mux"
	"log"
	"net/http"
)

func handleError(w http.ResponseWriter, err error, code int) {
	Logger.Log(NewLogMessage(
		ERROR,
		LogContext{
			"error": fmt.Sprintf("%s", err),
		},
		func() string {
			return fmt.Sprintf("[%v]", w)
		},
	))
	w.WriteHeader(code)
}

func shutdownHttpHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(200)
	w.Write([]byte("{\"message\": \"shutting down server\"}"))
	Shutdown()
}

func versionHttpHandler(w http.ResponseWriter, r *http.Request) {
	v := GetVersion()
	str, err := json.Marshal(v)
	if err != nil {
		handleError(w, err, 500)
	}

	if _, err := w.Write([]byte(str)); err != nil {
		handleError(w, err, 500)
	}
}

func configHttpHandler(w http.ResponseWriter, r *http.Request) {
	conf := GetConfiguration()
	str, err := json.Marshal(conf)
	if err != nil {
		handleError(w, err, 500)
		return
	}
	_, err = w.Write([]byte(str))
	if err != nil {
		handleError(w, err, 500)
	}
}

func addPratchettHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Clacks-Overhead", "GNU Terry Pratchett")
		next.ServeHTTP(w, r)
	})
}

func setContentTypeHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

var HttpServer *http.Server

func InitApi() {
	conf := GetConfiguration()
	router := mux.NewRouter().StrictSlash(true)
	InitPrometheus(router)
	router.Use(addPratchettHeader)
	router.Use(setContentTypeHeader)

	router.HandleFunc("/v1/config", configHttpHandler)
	router.HandleFunc("/v1/shutdown", shutdownHttpHandler)
	router.HandleFunc("/v1/version", versionHttpHandler)
	log.Printf("starting HTTP server on ':%d'\n", conf.HttpPort)
	HttpServer := &http.Server{Handler: router, Addr: fmt.Sprintf(":%d", conf.HttpPort)}
	// don't block the main thread with this jazz
	go func() {
		log.Printf(fmt.Sprintf("%s", HttpServer.ListenAndServe()))
	}()
}
