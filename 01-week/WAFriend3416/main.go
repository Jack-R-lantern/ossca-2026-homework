package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"syscall"
)

type CommandRequest struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

func main() {
	http.HandleFunc("/unshare/netns", func(w http.ResponseWriter, r *http.Request) {
		var reqData CommandRequest
		json.NewDecoder(r.Body).Decode(&reqData)

		err := syscall.Unshare(syscall.CLONE_NEWNET)
		if err != nil {
			http.Error(w, `{"error": "unshare failed"}`, http.StatusInternalServerError)
			return
		}

		cmd := exec.Command(reqData.Path, reqData.Args...)
		err = cmd.Start()
		if err != nil {
			http.Error(w, `{"error": "cmd start failed"}`, http.StatusInternalServerError)
			return
		}

		go func() {
			_ = cmd.Wait()
		}()

		// 응답
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{
			"parent_pid": os.Getpid(),
			"child_pid":  cmd.Process.Pid,
		})
	})

	fmt.Println("http://localhost:8080 에서 대기 중... 💣")
	http.ListenAndServe(":8080", nil)
}
