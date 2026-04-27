package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"syscall"
)

// Request body JSON 구조체
type RequestPayload struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

// Response body JSON 구조체
type ResponsePayload struct {
	ParentPID int `json:"parent_pid"`
	ChildPID  int `json:"child_pid"`
}

func netnsHandler(w http.ResponseWriter, r *http.Request) {
	// POST 메서드만 허용
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// JSON Request Body 파싱
	var req RequestPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	// 실행할 명령어 객체 생성
	cmd := exec.Command(req.Path, req.Args...)

	// 1. 자식 프로세스를 새로운 Network Namespace(CLONE_NEWNET)에서 실행
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWNET,
	}

	// 프로세스 시작 (완료될 때까지 기다리지 않음)
	if err := cmd.Start(); err != nil {
		http.Error(w, "Failed to start process: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 좀비 프로세스 방지를 위한 Wait 처리 (비동기)
	// API 응답을 먼저 보내야 하므로 goroutine을 사용하여 백그라운드에서 대기합니다.
	go func() {
		err := cmd.Wait()
		if err != nil {
			log.Printf("Child process %d exited with error: %v\n", cmd.Process.Pid, err)
		} else {
			log.Printf("Child process %d exited successfully\n", cmd.Process.Pid)
		}
	}()

	// Response JSON 생성
	res := ResponsePayload{
		ParentPID: os.Getpid(),
		ChildPID:  cmd.Process.Pid,
	}

	// 응답 반환
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