package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const listenAddr = ":8080"

// service는 HTTP handler와 Linux namespace 구현을 분리한다.
// handler는 요청/응답만 다루고, 실제 netns/veth/exec 작업은 service_linux.go에서 처리한다.
type service interface {
	CreateNetns(name string) (string, error)
	CreateVeth(name string, req vethRequest) (vethResponse, error)
	ExecInNetns(name string, req execRequest) (execResponse, error)
}

type app struct {
	service service
}

type createNetnsRequest struct {
	Name string `json:"name"`
}

type createNetnsResponse struct {
	Name      string `json:"name"`
	NetnsPath string `json:"netns_path"`
}

type vethRequest struct {
	HostIfname string `json:"host_ifname"`
	PeerIfname string `json:"peer_ifname"`
	HostIP     string `json:"host_ip"`
	PeerIP     string `json:"peer_ip"`
}

type vethResponse struct {
	Name       string `json:"name"`
	HostIfname string `json:"host_ifname"`
	PeerIfname string `json:"peer_ifname"`
	HostIP     string `json:"host_ip"`
	PeerIP     string `json:"peer_ip"`
	NetnsPath  string `json:"netns_path"`
}

type execRequest struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

type execResponse struct {
	Name      string `json:"name"`
	ParentPID int    `json:"parent_pid"`
	ChildPID  int    `json:"child_pid"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func main() {
	handler := newHandler(newService())
	server := &http.Server{
		Addr:              listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 3 * time.Second,
	}

	log.Printf("week 2 server listening on %s", listenAddr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func newHandler(svc service) http.Handler {
	a := &app{service: svc}

	// "/"는 서버가 정상적으로 떠 있는지 확인하기 위한 간단한 상태 확인 endpoint다.
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleRoot)
	mux.HandleFunc("/netns", a.handleNetns)
	mux.HandleFunc("/netns/", a.handleNetnsAction)

	return mux
}

func (a *app) handleRoot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *app) handleNetns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// /netns는 named network namespace의 이름만 받는다.
	// 실제 생성과 bind mount는 Linux 전용 service 구현에서 수행한다.
	var req createNetnsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := validateName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	path, err := a.service.CreateNetns(req.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, createNetnsResponse{
		Name:      req.Name,
		NetnsPath: path,
	})
}

func (a *app) handleNetnsAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// /netns/{name}/veth 또는 /netns/{name}/exec 형태만 허용한다.
	name, action, err := parseNetnsAction(r.URL.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	if err := validateName(name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	switch action {
	case "veth":
		a.handleVeth(w, r, name)
	case "exec":
		a.handleExec(w, r, name)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (a *app) handleVeth(w http.ResponseWriter, r *http.Request, name string) {
	// veth API는 host 쪽 interface와 namespace 쪽 interface 설정값을 한 번에 받는다.
	// route, bridge, NAT는 과제 범위가 아니므로 여기서 다루지 않는다.
	var req vethRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.HostIfname == "" || req.PeerIfname == "" || req.HostIP == "" || req.PeerIP == "" {
		writeError(w, http.StatusBadRequest, "host_ifname, peer_ifname, host_ip, and peer_ip are required")
		return
	}

	resp, err := a.service.CreateVeth(name, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (a *app) handleExec(w http.ResponseWriter, r *http.Request, name string) {
	// exec API는 이미 만들어진 named namespace 안에서 새 프로세스를 시작한다.
	// echoserver 검증에서는 이 child_pid를 checker가 다시 확인한다.
	var req execRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	resp, err := a.service.ExecInNetns(name, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func decodeJSON(r *http.Request, v any) error {
	decoder := json.NewDecoder(r.Body)
	// 과제에서 정의하지 않은 필드가 들어오면 잘못된 요청으로 처리한다.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}

	return nil
}

func parseNetnsAction(path string) (string, string, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 3 || parts[0] != "netns" {
		return "", "", errors.New("invalid netns path")
	}

	return parts[1], parts[2], nil
}

func validateName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}

	// name은 /var/run/netns/{name} 파일명으로 쓰이므로 경로 문자를 막는다.
	if strings.Contains(name, "/") || strings.Contains(name, "..") {
		return errors.New("name must not contain / or ..")
	}

	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	log.Printf("request failed: status=%d error=%s", status, msg)
	writeJSON(w, status, errorResponse{Error: msg})
}

func currentPID() int {
	return os.Getpid()
}
