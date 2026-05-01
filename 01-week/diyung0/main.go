package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"syscall"
)

// 요청 body 구조체
type RequestBody struct {
	Path string `json:"path"`
	Args []string `json:"args"`
}

// 응답 body 구조체
type ResponseBody struct {
	ParentPid int `json:"parent_pid"`
	ChildPid int `json:"child_pid"` 
}

func handleNetNs(w http.ResponseWriter, r *http.Request) {
	// POST 요청이 아닌 다른 method는 거절
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// 요청 body JSON 파싱
	var req RequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// path는 절대 경로여야 함
	if req.Path == "" || req.Path[0] != '/' {
		http.Error(w, "path must be absolute", http.StatusBadRequest)
		return
	}

	// 자식 프로세스 준비
	cmd := exec.Command(req.Path, req.Args...)

	// 자식 프로세스만 새 network namespace에서 시작
	cmd.SysProcAttr = &syscall.SysProcAttr{
			Unshareflags: syscall.CLONE_NEWNET, // 자식이 exec하기 직전에 unshare() syscall을 호출
	}

	// 자식 프로세스 실행
	if err := cmd.Start(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// zombie 프로세스 방지
	go func() {
			cmd.Wait()
	}()

	// 응답 JSON 작성
	res := ResponseBody{
			ParentPid: os.Getpid(),
			ChildPid: cmd.Process.Pid,
	}

	// Content-Type 헤더 설정 후 JSON 인코딩해서 응답
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(res)
}

func main() {
	// /unshare/netns 경로로 요청 오면 handleNetNs 실행
	http.HandleFunc("/unshare/netns", handleNetNs)

	// 8080 포트로 listen 시작
	http.ListenAndServe(":8080", nil)
}