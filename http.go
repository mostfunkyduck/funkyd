package main

import (
	"encoding/json"
	"fmt"
	"github.com/gorilla/mux"
	"log"
	"net/http"
)

func handleError(w http.ResponseWriter, err error, code int) {
	log.Printf("error, returning %s: [%s]", code, err)
	w.WriteHeader(code)
}

func config(w http.ResponseWriter, r *http.Request) {
	conf := GetConfiguration()
	str, err := json.Marshal(conf)
	if err != nil {
		handleError(w, err, 500)
		return
	}
	fmt.Fprintf(w, "%s\n", str)
}

func InitApi() {
	conf := GetConfiguration()
	router := mux.NewRouter().StrictSlash(true)
	InitPrometheus(router)
	router.HandleFunc("/config", config)
	log.Printf("starting HTTP server on ':%d'\n", conf.HttpPort)
	// don't block the main thread with this jazz
	go func() {
		log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", conf.HttpPort), router))
	}()
}
