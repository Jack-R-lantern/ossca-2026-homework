package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

type CommandRequest struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

type CommandStartResult struct {
	ChildPID int
	Error    string
}

const sysSetns = 308

func main() {
	http.HandleFunc("/unshare/netns", func(w http.ResponseWriter, r *http.Request) {
		var reqData CommandRequest
		json.NewDecoder(r.Body).Decode(&reqData)

		resultCh := make(chan CommandStartResult, 1)
		path := reqData.Path
		args := append([]string(nil), reqData.Args...)

		go func() {
			runtime.LockOSThread()

			origNS, err := os.Open(fmt.Sprintf("/proc/self/task/%d/ns/net", syscall.Gettid()))
			if err != nil {
				resultCh <- CommandStartResult{Error: "open original netns failed"}
				return
			}
			defer origNS.Close()

			err = syscall.Unshare(syscall.CLONE_NEWNET)
			if err != nil {
				resultCh <- CommandStartResult{Error: "unshare failed"}
				return
			}

			cmd := exec.Command(path, args...)
			err = cmd.Start()
			if err != nil {
				_ = setns(int(origNS.Fd()), syscall.CLONE_NEWNET)
				resultCh <- CommandStartResult{Error: "cmd start failed"}
				return
			}

			go func() {
				_ = cmd.Wait()
			}()

			if err := setns(int(origNS.Fd()), syscall.CLONE_NEWNET); err != nil {
				resultCh <- CommandStartResult{Error: "setns failed"}
				return
			}

			resultCh <- CommandStartResult{ChildPID: cmd.Process.Pid}
		}()

		result := <-resultCh
		if result.Error != "" {
			http.Error(w, `{"error": "`+result.Error+`"}`, http.StatusInternalServerError)
			return
		}

		// 응답
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{
			"parent_pid": os.Getpid(),
			"child_pid":  result.ChildPID,
		})
	})

	fmt.Println("http://localhost:8080 에서 대기 중... 💣")
	http.ListenAndServe(":8080", nil)
}

func setns(fd int, nstype int) error {
	_, _, errno := syscall.RawSyscall(sysSetns, uintptr(fd), uintptr(nstype), 0)
	if errno != 0 {
		return errno
	}
	return nil
}
