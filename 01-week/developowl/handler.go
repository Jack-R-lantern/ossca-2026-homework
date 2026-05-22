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

// unshareRequest는 /unshare/netns 요청 정보를 위한 구조체
type unshareRequest struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

// unshareResponse는 /unshare/netns 응답 정보를 위한 구조체
type unshareResponse struct {
	ParentPID int `json:"parent_pid"`
	ChildPID  int `json:"child_pid"`
}

// errorResponse는 에러 정보를 반환하는 구조체
type errorResponse struct {
	Error string `json:"error"`
}

// HandleUnshareNetns는 /unshare/netns 엔드포인트에서 처리할 로직을 담은 핸들러
func HandleUnshareNetns(w http.ResponseWriter, r *http.Request) {
	// Method 유형 체크
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	defer r.Body.Close()

	// Request 형식 검증
	var req unshareRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	// Request path 절대 경로 확인
	if !filepath.IsAbs(req.Path) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "path must be an absolute path"})
		return
	}

	// 자식 프로세스 인자로 unshare flag로 network namespace 할당
	cmd := exec.Command(req.Path, req.Args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Unshareflags: syscall.CLONE_NEWNET,
	}

	// 자식 프로세스의 객체에 맞게 프로세스 생성
	if err := cmd.Start(); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{
			Error: fmt.Sprintf("failed to start process in new netns: %v", err),
		})
		return
	}

	// parent, child pid 가져옴
	childPID := cmd.Process.Pid
	parentPID := os.Getpid()

	// zombie 방지
	// 좀비 프로세스: 자식 프로세스의 실행이 끝났음에도 부모 프로세스에 자식 프로세스 정보가 남아 있음
	// goroutine을 통해 빠르게 응답을 받돼 백그라운드에서 좀비 프로세스를 정리함
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("child %d exited with error: %v", cmd.Process.Pid, err)
		}
	}()

	// 응답값 반환
	writeJSON(w, http.StatusOK, unshareResponse{
		ParentPID: parentPID,
		ChildPID:  childPID,
	})
}

// writeJSON는 여러 정보를 JSON 형태로 반환
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
