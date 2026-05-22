package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type response struct {
	Message      string `json:"message"`
	PID          int    `json:"pid"`
	NamespaceRef string `json:"namespace_ref"`
	LocalAddr    string `json:"local_addr"`
	RemoteAddr   string `json:"remote_addr"`
	Method       string `json:"method"`
	Path         string `json:"path"`
	ReceivedAt   string `json:"received_at"`
}

func main() {
	namespaceRef, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response{
			Message:      "request reached the process running inside the named network namespace",
			PID:          os.Getpid(),
			NamespaceRef: namespaceRef,
			LocalAddr:    localAddr(r),
			RemoteAddr:   r.RemoteAddr,
			Method:       r.Method,
			Path:         r.URL.Path,
			ReceivedAt:   time.Now().Format(time.RFC3339),
		})
	})

	server := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("echoserver listening on :8080")

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func localAddr(r *http.Request) string {
	addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok || addr == nil {
		return r.Host
	}

	return addr.String()
}
