package main

import (
	"errors"
	"log"
	"net/http"
)

// unshareRequest는 /unshare/netns 요청 정보를 위한 구조체
type unshareRequest struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

// unshareResponse는 /unshare/netns 응답 정보를 위한 구조체
type unshareResponse struct {
	ParentPID int `json:"parent_pid"`
	ChildPID  int `json:"child_pid"`
}

// errorResponse는 에러 정보를 반환하는 구조체
type errorResponse struct {
	Error string `json:"error"`
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/unshare/netns", HandleUnshareNetns)

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	log.Printf("server listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server failed: %v", err)
	}
}
