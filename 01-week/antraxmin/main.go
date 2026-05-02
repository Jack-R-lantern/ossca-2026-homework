package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"syscall"
)

type Request struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

type Response struct {
	ParentPID int `json:"parent_pid"`
	ChildPID  int `json:"child_pid"`
}

func handler(w http.ResponseWriter, r *http.Request) {
	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return
	}

	cmd := exec.Command(req.Path, req.Args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWNET,
	}

	if err := cmd.Start(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	go cmd.Wait()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{
		ParentPID: os.Getpid(),
		ChildPID:  cmd.Process.Pid,
	})
}

func main() {
	http.HandleFunc("/unshare/netns", handler)
	http.ListenAndServe(":8080", nil)
}
