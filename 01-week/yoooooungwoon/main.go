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

func handleNetns(w http.ResponseWriter, r *http.Request) {
	var req Request
	json.NewDecoder(r.Body).Decode(&req)

	cmd := exec.Command(req.Path, req.Args...)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Unshareflags: syscall.CLONE_NEWNET,
	}

	cmd.Start()
	go cmd.Wait()

	resp := Response{
		ParentPID: os.Getpid(),
		ChildPID:  cmd.Process.Pid,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func main() {
	http.HandleFunc("/unshare/netns", handleNetns)
	http.ListenAndServe(":8080", nil)
}