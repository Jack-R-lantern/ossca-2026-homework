//go:build linux

package main

import (
	"log"
	"net/http"
	"time"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/netns", handleCreateNetns)
	mux.HandleFunc("/netns/", handleNetnsAction)

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
	}

	log.Printf("02-week assignment server listening on %s", listenAddr)
	log.Fatal(server.ListenAndServe())
}
