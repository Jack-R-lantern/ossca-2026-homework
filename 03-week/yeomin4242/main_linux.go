//go:build linux

package main

import (
	"log"
	"net/http"
	"time"
)

func main() {
	manager := newBPFManager()
	defer manager.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/bpf/attach", manager.handleAttach)
	mux.HandleFunc("/bpf/block/", manager.handleBlock)
	mux.HandleFunc("/bpf/clear/", manager.handleClear)

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
	}

	log.Printf("03-week XDP assignment server listening on %s", listenAddr)
	log.Fatal(server.ListenAndServe())
}
