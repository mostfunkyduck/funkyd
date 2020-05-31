package main

// HTTP admin API

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/mux"
)

// nolint:unparam
func handleError(w http.ResponseWriter, err error, code int) {
	Logger.Log(LogMessage{
		Level: ERROR,
		Context: LogContext{
			"error": fmt.Sprintf("%s", err),
		},
	})
	w.WriteHeader(code)
}

func shutdownHTTPHandler(w http.ResponseWriter, r *http.Request) {
	// nolint:gomnd
	w.WriteHeader(200)
	_, err := w.Write([]byte("{\"message\": \"shutting down server\"}"))
	if err != nil {
		Logger.Log(LogMessage{
			Level: ERROR,
			Context: LogContext{
				"what":  "error shutting down server",
				"error": err.Error(),
				"next":  "continuing shut down",
			},
		})
	}
	Shutdown()
}

func versionHTTPHandler(w http.ResponseWriter, r *http.Request) {
	v := GetVersion()
	str, err := json.Marshal(v)
	if err != nil {
		handleError(w, err, 500)
	}

	if _, err := w.Write(str); err != nil {
		handleError(w, err, 500)
	}
}

func configHTTPHandler(w http.ResponseWriter, r *http.Request) {
	conf := GetConfiguration()
	str, err := json.Marshal(conf)
	if err != nil {
		handleError(w, err, 500)
		return
	}
	_, err = w.Write(str)
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

//nolint
var HTTPServer *http.Server

func InitAPI() {
	conf := GetConfiguration()
	router := mux.NewRouter().StrictSlash(true)
	InitPrometheus(router)
	router.Use(addPratchettHeader)
	router.Use(setContentTypeHeader)

	router.HandleFunc("/v1/config", configHTTPHandler)
	router.HandleFunc("/v1/shutdown", shutdownHTTPHandler)
	router.HandleFunc("/v1/version", versionHTTPHandler)
	log.Printf("starting HTTP server on ':%d'\n", conf.HttpPort)
	HTTPServer := &http.Server{Handler: router, Addr: fmt.Sprintf(":%d", conf.HttpPort)}
	// don't block the main thread with this jazz
	go func() {
		if err := HTTPServer.ListenAndServe(); err != nil {
			log.Fatalf("error on http server: %s", err)
		}
	}()
}
