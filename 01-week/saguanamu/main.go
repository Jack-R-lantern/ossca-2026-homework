package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

type UnshareNetnsReq struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

type UnshareNetnsRes struct {
	ParentPID int `json:"parent_pid"`
	ChildPID  int `json:"child_pid"`
}

func main() {
	http.HandleFunc("/unshare/netns", handleUnshareNetns)

	log.Println("listening on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}

func handleUnshareNetns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req UnshareNetnsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}

	if req.Path == "" || !filepath.IsAbs(req.Path) {
		http.Error(w, "path must be absolute", http.StatusBadRequest)
		return
	}

	cmd := exec.Command(req.Path, req.Args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// Unshareflags: 부모와 자식이 같은 속성인데 자식만 부모 netns 분리
		Unshareflags: syscall.CLONE_NEWNET,
	}

	if err := cmd.Start(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("child process exited with error: %v", err)
		}
	}()

	parentNS := getNetNamespace(os.Getpid())
	childNS := getNetNamespace(cmd.Process.Pid)

	log.Printf("[REQ] path=%s args=%v", req.Path, req.Args)
	log.Printf("[NS] parent_pid=%d parent_netns=%s", os.Getpid(), parentNS)
	log.Printf("[NS] child_pid=%d child_netns=%s", cmd.Process.Pid, childNS)

	res := UnshareNetnsRes{
		ParentPID: os.Getpid(),
		ChildPID:  cmd.Process.Pid,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func getNetNamespace(pid int) string {
	nsPath := fmt.Sprintf("/proc/%d/ns/net", pid)

	link, err := os.Readlink(nsPath)
	if err != nil {
		return "unknown"
	}

	return link
}
