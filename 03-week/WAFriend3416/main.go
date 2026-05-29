package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const listenAddr = ":8080"

// service는 HTTP 계층과 실제 XDP/eBPF 구현을 분리하기 위한 인터페이스다.
// Linux에서는 service_linux.go가 실제 attach/map 조작을 수행하고,
// macOS 같은 non-Linux 환경에서는 service_unsupported.go가 같은 메서드 형태만 제공한다.
type service interface {
	Attach(ifname string) (attachResponse, error)
	Block(ifname string, ip string) (blockResponse, error)
	Clear(ifname string) (clearResponse, error)
	Close() error
}

type app struct {
	service service
}

type attachRequest struct {
	IfName string `json:"ifname"`
}

type attachResponse struct {
	IfName   string `json:"ifname"`
	Hook     string `json:"hook"`
	Attached bool   `json:"attached"`
}

type ipRequest struct {
	IP string `json:"ip"`
}

type blockResponse struct {
	IfName    string `json:"ifname"`
	BlockedIP string `json:"blocked_ip"`
}

type clearResponse struct {
	IfName  string `json:"ifname"`
	Cleared bool   `json:"cleared"`
}

type errorResponse struct {
	Error string `json:"error"`
}

type statusError struct {
	status int
	msg    string
}

func (e statusError) Error() string {
	return e.msg
}

func main() {
	// HTTP handler는 요청/응답만 처리하고, 실제 XDP 작업은 OS별 service 구현에 맡긴다.
	svc := newService()
	defer func() {
		if err := svc.Close(); err != nil {
			log.Printf("close service: %v", err)
		}
	}()

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           newHandler(svc),
		ReadHeaderTimeout: 3 * time.Second,
	}

	// Ctrl+C 또는 SIGTERM을 받으면 HTTP server를 먼저 종료한 뒤 service.Close에서 XDP link를 정리한다.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("week 3 xdp server listening on %s", listenAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func newHandler(svc service) http.Handler {
	a := &app{service: svc}

	// 3주차 과제에서 checker가 호출하는 BPF API만 등록한다.
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleRoot)
	mux.HandleFunc("/bpf/attach", a.handleAttach)
	mux.HandleFunc("/bpf/block/", a.handleBlock)
	mux.HandleFunc("/bpf/clear/", a.handleClear)

	return mux
}

func (a *app) handleRoot(w http.ResponseWriter, _ *http.Request) {
	// checker가 직접 요구하지는 않지만, 서버 기동 여부를 확인하기 위한 간단한 health endpoint다.
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *app) handleAttach(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// attach 요청은 host namespace에 이미 존재하는 veth interface 이름만 받는다.
	var req attachRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 응답 형식은 checker가 그대로 비교하므로 service에서 과제 명세와 같은 필드를 돌려준다.
	resp, err := a.service.Attach(req.IfName)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (a *app) handleBlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// /bpf/block/{ifname}의 ifname은 차단 규칙을 적용할 attach된 interface다.
	ifname, err := parseBPFPath(r.URL.Path, "/bpf/block/")
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	var req ipRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// block 요청은 이미 attach된 interface에 대해서만 성공해야 한다.
	// 이 검증은 service가 attach 상태를 알고 있으므로 service.Block 안에서 처리한다.
	resp, err := a.service.Block(ifname, req.IP)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (a *app) handleClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// clear는 blocked IP 목록만 비우며, XDP attach 자체는 유지한다.
	ifname, err := parseBPFPath(r.URL.Path, "/bpf/clear/")
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	// README상 clear body는 필요 없고, checker가 body를 보내더라도 여기서는 읽지 않는다.
	// clear의 의미는 "해당 interface의 blocked IP 목록만 비우기"이기 때문이다.
	resp, err := a.service.Clear(ifname)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func decodeJSON(r *http.Request, v any) error {
	decoder := json.NewDecoder(r.Body)
	// 과제에서 정의하지 않은 필드는 잘못된 요청으로 처리한다.
	decoder.DisallowUnknownFields()
	return decoder.Decode(v)
}

func parseBPFPath(path string, prefix string) (string, error) {
	// ServeMux가 prefix 기반으로 handler를 호출하므로, 실제로 기대한 endpoint인지 다시 확인한다.
	if !strings.HasPrefix(path, prefix) {
		return "", errors.New("invalid bpf path")
	}

	raw := strings.TrimPrefix(path, prefix)
	// /bpf/block/{ifname}/extra 같은 경로를 잘못된 요청으로 처리하기 위해 slash를 허용하지 않는다.
	if raw == "" || strings.Contains(raw, "/") {
		return "", errors.New("invalid interface path")
	}

	// interface 이름에 URL escaping이 들어온 경우를 위해 한 번 복원한다.
	ifname, err := url.PathUnescape(raw)
	if err != nil {
		return "", err
	}

	return ifname, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeServiceError(w http.ResponseWriter, err error) {
	var statusErr statusError
	if errors.As(err, &statusErr) {
		// 잘못된 요청과 실제 서버 오류를 구분해 checker가 읽을 수 있는 JSON error를 내려준다.
		writeError(w, statusErr.status, statusErr.msg)
		return
	}

	writeError(w, http.StatusInternalServerError, err.Error())
}

func writeError(w http.ResponseWriter, status int, msg string) {
	log.Printf("request failed: status=%d error=%s", status, msg)
	writeJSON(w, status, errorResponse{Error: msg})
}
