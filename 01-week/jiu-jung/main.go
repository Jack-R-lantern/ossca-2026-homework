package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

type unshareRequest struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

type unshareResponse struct {
	ParentPID int `json:"parent_pid"`
	ChildPID  int `json:"child_pid"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func main() {
	http.HandleFunc("/unshare/netns", handleUnshareNetNS)

	log.Println("server listening on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}

func handleUnshareNetNS(w http.ResponseWriter, r *http.Request) {
	// POST 메서드만 허용
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req unshareRequest

	// JSON 요청 본문 파싱
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	// 요청 값 검증
	if err := validateUnshareRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// path와 args로 실행 명령 생성
	cmd := exec.Command(req.Path, req.Args...)

	// child process에서 network namespace 분리
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Unshareflags: syscall.CLONE_NEWNET,
	}

	// child process 시작
	if err := cmd.Start(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	parentPID := os.Getpid()
	childPID := cmd.Process.Pid

	// child 종료 후 zombie 방지
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("child wait error: pid=%d err=%v", childPID, err)
		}
	}()

	resp := unshareResponse{
		ParentPID: parentPID,
		ChildPID:  childPID,
	}

	// JSON 응답 반환
	writeJSON(w, http.StatusOK, resp)
}

func validateUnshareRequest(req unshareRequest) error {
	if req.Path == "" {
		return errors.New("path is required")
	}

	if !filepath.IsAbs(req.Path) {
		return errors.New("path must be an absolute path")
	}

	info, err := os.Stat(req.Path)
	if err != nil {
		return errors.New("path does not exist")
	}

	if info.IsDir() {
		return errors.New("path must be an executable file, not directory")
	}

	if info.Mode()&0111 == 0 {
		return errors.New("path is not executable")
	}

	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("failed to write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{
		Error: message,
	})
}
