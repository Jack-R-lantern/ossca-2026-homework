package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// HandleUnshareNetns는 /net/ns 엔드포인트에서 처리할 로직을 담은 핸들러
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

	// Request 조건 검증
	if err := validateRequest(req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
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

// writeJson는 여러 정보를 Json 형태로 반환
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// vaildateRequest는 요청 값을 조건으로 검증
// 아래의 조건을 구현:
//   - path는 실행 파일의 절대 경로여야 한다.
//   - shell command string 형태는 허용하지 않는다.
func validateRequest(req unshareRequest) error {
	// 절대 경로만 사용
	if !filepath.IsAbs(req.Path) {
		return errors.New("path must be an absolute path")
	}

	// shell string 방지
	// ex) /bin/sh -c 'sleep 30'
	base := filepath.Base(req.Path)
	if (base == "sh" || base == "bash" || base == "zsh") && len(req.Args) > 0 && req.Args[0] == "-c" {
		return errors.New("shell command string is not allowed")
	}

	return nil
}
