//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

const (
	netnsDir = "/var/run/netns"
)

type NetnsRequest struct {
	Name string `json:"name"`
}

type NetnsResponse struct {
	Name      string `json:"name"`
	NetnsPath string `json:"netns_path"`
}

type VethRequest struct {
	HostIfname string `json:"host_ifname"`
	PeerIfname string `json:"peer_ifname"`
	HostIP     string `json:"host_ip"`
	PeerIP     string `json:"peer_ip"`
}

type ExecRequest struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

type ExecResponse struct {
	Name      string `json:"name"`
	ParentPID int    `json:"parent_pid"`
	ChildPID  int    `json:"child_pid"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/netns", handleNetnsCreate)
	mux.HandleFunc("/netns/", handleNetnsSub)

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	log.Println("API server listening on :8080")
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func handleNetnsCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req NetnsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := validateName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	path, err := createNamedNetns(req.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := NetnsResponse{
		Name:      req.Name,
		NetnsPath: path,
	}

	writeJSON(w, http.StatusOK, resp)
}

func handleNetnsSub(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	trimmed := strings.TrimPrefix(path, "/netns/")
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}

	name := parts[0]
	action := parts[1]

	if err := validateName(name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if action == "veth" {
		handleVethCreate(w, r, name)
	} else if action == "exec" {
		handleExec(w, r, name)
	} else {
		http.NotFound(w, r)
	}
}

func handleVethCreate(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req VethRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.HostIfname == "" || req.PeerIfname == "" || req.HostIP == "" || req.PeerIP == "" {
		writeError(w, http.StatusBadRequest, "all veth fields are required")
		return
	}

	if err := createVeth(name, req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// JSON response for host namespace and named network namespace communication testing
	resp := map[string]string{
		"name":        name,
		"host_ifname": req.HostIfname,
		"peer_ifname": req.PeerIfname,
		"host_ip":     req.HostIP,
		"peer_ip":     req.PeerIP,
		"netns_path":  filepath.Join(netnsDir, name),
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleExec(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req ExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	parentPID, childPID, err := execInNetns(name, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := ExecResponse{
		Name:      name,
		ParentPID: parentPID,
		ChildPID:  childPID,
	}

	writeJSON(w, http.StatusOK, resp)
}

func execInNetns(name string, req ExecRequest) (int, int, error) {
	targetNS, err := netns.GetFromName(name)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to open named netns: %v", err)
	}
	defer targetNS.Close()

	// netns.Set()도 현재 OS thread에 적용되므로 thread를 고정한다.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	originNS, err := netns.Get()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get origin netns: %v", err)
	}
	defer originNS.Close()

	if err := netns.Set(targetNS); err != nil {
		return 0, 0, fmt.Errorf("failed to enter named netns: %v", err)
	}

	// 프로세스 실행 후 즉시 원래 네임스페이스로 복귀하도록 defer 처리
	defer func() {
		_ = netns.Set(originNS)
	}()

	cmd := exec.Command(req.Path, req.Args...)
	// 부모 터미널에서 실행된 프로세스의 출력을 볼 수 있게 바인딩
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return 0, 0, fmt.Errorf("failed to start process in named netns: %v", err)
	}

	// 백그라운드에서 프로세스가 종료될 때 리소스를 거둬들이도록(wait) 고루틴 실행
	go func() {
		_ = cmd.Wait()
	}()

	return os.Getpid(), cmd.Process.Pid, nil
}

// --- Helpers for Netns ---

func createNamedNetns(name string) (string, error) {
	path := filepath.Join(netnsDir, name)

	if err := os.MkdirAll(netnsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create netns dir: %v", err)
	}

	if _, err := os.Stat(path); err == nil {
		if isNSFSMount(path) {
			return path, nil
		}
		return "", fmt.Errorf("netns path already exists but is not nsfs mount: %s", path)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to stat netns path: %v", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL, 0444)
	if err != nil {
		return "", fmt.Errorf("failed to create netns mount point: %v", err)
	}
	_ = f.Close()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	originNS, err := netns.Get()
	if err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("failed to get origin netns: %v", err)
	}
	defer originNS.Close()

	newNS, err := netns.New()
	if err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("failed to create new network namespace: %v", err)
	}
	defer newNS.Close()

	defer func() {
		_ = netns.Set(originNS)
	}()

	tid := unix.Gettid()
	threadNsPath := fmt.Sprintf("/proc/self/task/%d/ns/net", tid)

	if err := unix.Mount(threadNsPath, path, "", unix.MS_BIND, ""); err != nil {
		_ = netns.Set(originNS)
		_ = os.Remove(path)
		return "", fmt.Errorf("failed to bind mount named netns: %v", err)
	}

	return path, nil
}

// --- Helpers for Veth ---

func createVeth(name string, req VethRequest) error {
	targetNS, err := netns.GetFromName(name)
	if err != nil {
		return fmt.Errorf("failed to open named netns: %v", err)
	}
	defer targetNS.Close()

	if err := deleteLinkIfExists(req.HostIfname); err != nil {
		return fmt.Errorf("failed to delete existing host link: %v", err)
	}

	if err := withNetns(targetNS, func() error {
		return deleteLinkIfExists(req.PeerIfname)
	}); err != nil {
		return err
	}

	tmpPeerName := makeTempPeerName(req.HostIfname)
	if err := deleteLinkIfExists(tmpPeerName); err != nil {
		return fmt.Errorf("failed to delete existing temp peer link: %v", err)
	}

	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{
			Name: req.HostIfname,
		},
		PeerName: tmpPeerName,
	}

	if err := netlink.LinkAdd(veth); err != nil {
		return fmt.Errorf("failed to add veth pair: %v", err)
	}

	hostLink, err := netlink.LinkByName(req.HostIfname)
	if err != nil {
		return fmt.Errorf("failed to find host veth: %v", err)
	}

	peerLink, err := netlink.LinkByName(tmpPeerName)
	if err != nil {
		return fmt.Errorf("failed to find peer veth: %v", err)
	}

	if err := netlink.LinkSetNsFd(peerLink, int(targetNS)); err != nil {
		return fmt.Errorf("failed to move peer veth to named netns: %v", err)
	}

	hostAddr, err := netlink.ParseAddr(req.HostIP)
	if err != nil {
		return fmt.Errorf("failed to parse host_ip: %v", err)
	}

	if err := netlink.AddrAdd(hostLink, hostAddr); err != nil && !isAlreadyExists(err) {
		return fmt.Errorf("failed to add host ip: %v", err)
	}

	if err := netlink.LinkSetUp(hostLink); err != nil {
		return fmt.Errorf("failed to set host veth up: %v", err)
	}

	if err := withNetns(targetNS, func() error {
		peerLink, err := netlink.LinkByName(tmpPeerName)
		if err != nil {
			return fmt.Errorf("failed to find peer veth in named netns: %v", err)
		}

		if err := deleteLinkIfExists(req.PeerIfname); err != nil {
			return err
		}

		if err := netlink.LinkSetName(peerLink, req.PeerIfname); err != nil {
			return fmt.Errorf("failed to rename peer veth: %v", err)
		}

		peerLink, err = netlink.LinkByName(req.PeerIfname)
		if err != nil {
			return fmt.Errorf("failed to find renamed peer veth: %v", err)
		}

		peerAddr, err := netlink.ParseAddr(req.PeerIP)
		if err != nil {
			return fmt.Errorf("failed to parse peer_ip: %v", err)
		}

		if err := netlink.AddrAdd(peerLink, peerAddr); err != nil && !isAlreadyExists(err) {
			return fmt.Errorf("failed to add peer ip: %v", err)
		}

		if err := netlink.LinkSetUp(peerLink); err != nil {
			return fmt.Errorf("failed to set peer veth up: %v", err)
		}

		loLink, err := netlink.LinkByName("lo")
		if err == nil {
			_ = netlink.LinkSetUp(loLink)
		}

		return nil
	}); err != nil {
		return err
	}

	return nil
}

// --- Common Helpers ---

func withNetns(targetNS netns.NsHandle, fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	originNS, err := netns.Get()
	if err != nil {
		return fmt.Errorf("failed to get origin netns: %v", err)
	}
	defer originNS.Close()

	if err := netns.Set(targetNS); err != nil {
		return fmt.Errorf("failed to enter target netns: %v", err)
	}

	defer func() {
		_ = netns.Set(originNS)
	}()

	return fn()
}

func deleteLinkIfExists(name string) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to lookup link %s: %v", name, err)
	}

	if err := netlink.LinkDel(link); err != nil {
		return fmt.Errorf("failed to delete existing link %s: %v", name, err)
	}

	time.Sleep(100 * time.Millisecond)
	return nil
}

func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if strings.Contains(name, "/") || strings.Contains(name, "..") {
		return fmt.Errorf("invalid namespace name: %s", name)
	}
	return nil
}

func makeTempPeerName(hostIfname string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(hostIfname))
	return fmt.Sprintf("tmp%x", h.Sum32())
}

func isNSFSMount(path string) bool {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return false
	}
	return st.Type == unix.NSFS_MAGIC
}

func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "file exists") || strings.Contains(msg, "object already exists")
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") ||
		strings.Contains(msg, "no such network interface") ||
		strings.Contains(msg, "link not found")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	log.Printf("request failed: status=%d error=%s", status, msg)
	writeJSON(w, status, ErrorResponse{Error: msg})
}
