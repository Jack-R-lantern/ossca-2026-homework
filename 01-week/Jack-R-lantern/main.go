package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
)

type UnshareNetnsRequest struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

type UnshareNetnsResponse struct {
	ParentPID int `json:"parent_pid"`
	ChildPID  int `json:"child_pid"`
}

func main() {
	http.HandleFunc("/unshare/netns", handleUnshareNetns)

	log.Println("server listening on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}

func handleUnshareNetns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req UnshareNetnsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}

	parentPID := os.Getpid()

	resp := UnshareNetnsResponse{
		ParentPID: parentPID,
		ChildPID:  parentPID,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
