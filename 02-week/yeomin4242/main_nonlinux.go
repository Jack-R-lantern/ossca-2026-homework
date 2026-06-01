//go:build !linux

package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

const listenAddr = ":8080"

type errorResponse struct {
	Error string `json:"error"`
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(errorResponse{
			Error: "network namespace operations require Linux",
		})
	})

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
	}

	log.Printf("02-week assignment server stub listening on %s", listenAddr)
	log.Fatal(server.ListenAndServe())
}
