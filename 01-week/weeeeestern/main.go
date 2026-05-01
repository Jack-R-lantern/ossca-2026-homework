package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

func main() {
	http.HandleFunc("/unshare/netns", handleUnshareNetns)

	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		panic(err)
	}
}

func handleUnshareNetns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "only POST is allowed", http.StatusMethodNotAllowed)
		return
	}

	var req Request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}

	if req.Path == "" || !filepath.IsAbs(req.Path) {
		http.Error(w, "path must be an absolute path", http.StatusBadRequest)
		return
	}

	cmd := exec.Command(req.Path, req.Args...)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Unshareflags: syscall.CLONE_NEWNET,
	} // Linux 의 network namespace 기능이므로, window 에서는 실행 불가

	err = cmd.Start()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	childPID := cmd.Process.Pid

	go func() {
		cmd.Wait()
	}()

	res := Response{
		ParentPID: os.Getpid(),
		ChildPID:  childPID,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
