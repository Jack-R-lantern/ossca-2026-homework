package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"syscall"
)

// Request body JSON structure
type RequestPayload struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

// Response body JSON structure
type ResponsePayload struct {
	ParentPID int `json:"parent_pid"`
	ChildPID  int `json:"child_pid"`
}

func netnsHandler(w http.ResponseWriter, r *http.Request) {
	// POST method only
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// JSON Request Body parsing
	var req RequestPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	if req.Path == "/bin/sh" && len(req.Args) == 2 && req.Args[0] == "-c"  {
		req.Args[1] = req.Args[1] + "; true" // for preventing sh terminating 

	}

	// CMD object generation
	cmd := exec.Command(req.Path, req.Args...)

	// 1. child process conducting in a new Network Namespace(CLONE_NEWNET)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWNET,
	}

	// process start (no waiting for completion)
	if err := cmd.Start(); err != nil {
		http.Error(w, "Failed to start process: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// preventing zombie process with waiting in background
	// goroutine for API response first and then waiting for child process to exit
	go func() {
		err := cmd.Wait()
		if err != nil {
			log.Printf("Child process %d exited with error: %v\n", cmd.Process.Pid, err)
		} else {
			log.Printf("Child process %d exited successfully\n", cmd.Process.Pid)
		}
	}()

	// Response JSON structure generation
	res := ResponsePayload{
		ParentPID: os.Getpid(),
		ChildPID:  cmd.Process.Pid,
	}

	// Return response with JSON format
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(res)
}

func main() {
	http.HandleFunc("/unshare/netns", netnsHandler)

	log.Println("Server is listening on port 8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}