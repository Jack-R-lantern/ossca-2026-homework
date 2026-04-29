package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"syscall"
)

type Req struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

func main() {
	pidCh := make(chan int)

	http.HandleFunc("/unshare/netns", func(w http.ResponseWriter, r *http.Request) {
		var req Req

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		if req.Path == "" || len(req.Args) == 0 {
			http.Error(w, "path and args are required", http.StatusBadRequest)
			return
		}

		pid, err := createNSContainer(req.Path, req.Args)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to create container: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{
			"parent_pid": os.Getpid(),
			"child_pid":  pid,
		})

		pidCh <- pid

	})

	go func() {
		if err := http.ListenAndServe(":8080", nil); err != nil {
			log.Fatal(err)
		}
	}()

	for pid := range pidCh {
		go func(p int) {
			var ws syscall.WaitStatus
			syscall.Wait4(p, &ws, 0, nil)
			log.Printf("pid %d exited", p)
		}(pid)
	}
}

func createNSContainer(path string, args []string) (int, error) {
	if path != "/bin/sh" {
		return -1, fmt.Errorf("path must be /bin/sh")
	}
	cmd := exec.Command(path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Unshareflags: syscall.CLONE_NEWNET,
	}

	if err := cmd.Start(); err != nil {
		return -1, err
	}

	return cmd.Process.Pid, nil
}
