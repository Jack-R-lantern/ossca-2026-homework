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
	listenAddr = ":8080"
	netnsDir   = "/var/run/netns"
)

type createNetnsRequest struct {
	Name string `json:"name"`
}

type createNetnsResponse struct {
	Name      string `json:"name"`
	NetnsPath string `json:"netns_path"`
}

type createVethRequest struct {
	HostIfname string `json:"host_ifname"`
	PeerIfname string `json:"peer_ifname"`
	HostIP     string `json:"host_ip"`
	PeerIP     string `json:"peer_ip"`
}

type createVethResponse struct {
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
	mux := http.NewServeMux()

	mux.HandleFunc("/netns", handleNetns)
	mux.HandleFunc("/netns/", handleNetnsSubresource)

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
	}

	log.Printf("assignment server listening on %s", listenAddr)

	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func handleNetns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req createNetnsRequest
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

	writeJSON(w, http.StatusOK, createNetnsResponse{
		Name:      req.Name,
		NetnsPath: path,
	})
}

func handleNetnsSubresource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

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
		handleVeth(w, r, name)
	case "exec":
		handleExec(w, r, name)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func handleVeth(w http.ResponseWriter, r *http.Request, name string) {
	var req createVethRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.HostIfname == "" || req.PeerIfname == "" || req.HostIP == "" || req.PeerIP == "" {
		writeError(w, http.StatusBadRequest, "host_ifname, peer_ifname, host_ip, peer_ip are required")
		return
	}

	if err := createVeth(name, req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, createVethResponse{
		Name:       name,
		HostIfname: req.HostIfname,
		PeerIfname: req.PeerIfname,
		HostIP:     req.HostIP,
		PeerIP:     req.PeerIP,
		NetnsPath:  netnsPath(name),
	})
}

func handleExec(w http.ResponseWriter, r *http.Request, name string) {
	var req execRequest
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

	writeJSON(w, http.StatusOK, execResponse{
		Name:      name,
		ParentPID: parentPID,
		ChildPID:  childPID,
	})
}

func createNamedNetns(name string) (string, error) {
	path := netnsPath(name)

	if err := os.MkdirAll(netnsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create netns dir: %w", err)
	}

	if _, err := os.Stat(path); err == nil {
		if isNSFSMount(path) {
			return path, nil
		}

		// path는 존재하지만 nsfs mount가 아니라면 실패 처리
		if err := os.Remove(path); err != nil {
			return "", fmt.Errorf("failed to remove stale netns path: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to stat netns path: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL, 0444)
	if err != nil {
		return "", fmt.Errorf("failed to create netns mount point: %w", err)
	}
	_ = f.Close()

	// network namespace 변경은 프로세스 전체가 아니라 스레드 단위로 적용된다.
	// Go runtime이 goroutine을 다른 스레드로 옮기지 않도록 현재 스레드에 고정한다.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	originNS, err := netns.Get()
	if err != nil {
		return "", fmt.Errorf("failed to get origin netns: %w", err)
	}
	defer originNS.Close()

	// netns.New()는 새 network namespace를 만들고 현재 스레드를 그 namespace로 이동시킨다.
	newNS, err := netns.New()
	if err != nil {
		return "", fmt.Errorf("failed to create new network namespace: %w", err)
	}
	defer newNS.Close()

	defer func() {
		_ = netns.Set(originNS)
	}()

	// 현재 스레드는 새 namespace 안에 있으므로 /proc/self/ns/net은 새 netns를 가리킨다.
	// 이를 /run/netns/{name}에 bind mount 해야 checker가 named namespace로 인식할 수 있다.
	if err := unix.Mount("/proc/self/ns/net", path, "", unix.MS_BIND, ""); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("failed to bind mount named netns: %w", err)
	}

	return path, nil
}

func createVeth(name string, req createVethRequest) error {
	targetNS, err := netns.GetFromPath(netnsPath(name))
	if err != nil {
		return fmt.Errorf("failed to open named netns: %w", err)
	}
	defer targetNS.Close()

	if oldHost, err := netlink.LinkByName(req.HostIfname); err == nil {
		_ = netlink.LinkDel(oldHost)
		time.Sleep(100 * time.Millisecond)
	}

	_ = withNetns(targetNS, func() error {
		if oldPeer, err := netlink.LinkByName(req.PeerIfname); err == nil {
			_ = netlink.LinkDel(oldPeer)
			time.Sleep(100 * time.Millisecond)
		}
		return nil
	})

	// peer 이름을 바로 eth0로 만들면 host namespace의 eth0와 충돌할 수 있다.
	// 그래서 임시 이름으로 만든 뒤 target namespace 안에서 최종 이름으로 변경한다.
	tmpPeerName := makeTempPeerName(req.HostIfname)

	if oldTmp, err := netlink.LinkByName(tmpPeerName); err == nil {
		_ = netlink.LinkDel(oldTmp)
		time.Sleep(100 * time.Millisecond)
	}

	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{
			Name: req.HostIfname,
		},
		PeerName: tmpPeerName,
	}

	if err := netlink.LinkAdd(veth); err != nil {
		return fmt.Errorf("failed to add veth pair: %w", err)
	}

	hostLink, err := netlink.LinkByName(req.HostIfname)
	if err != nil {
		return fmt.Errorf("failed to find host veth: %w", err)
	}

	peerLink, err := netlink.LinkByName(tmpPeerName)
	if err != nil {
		return fmt.Errorf("failed to find peer veth: %w", err)
	}

	// veth pair 중 peer 쪽 interface만 target network namespace로 이동시킨다.
	if err := netlink.LinkSetNsFd(peerLink, int(targetNS)); err != nil {
		return fmt.Errorf("failed to move peer veth to named netns: %w", err)
	}

	hostAddr, err := netlink.ParseAddr(req.HostIP)
	if err != nil {
		return fmt.Errorf("failed to parse host_ip: %w", err)
	}

	if err := netlink.AddrAdd(hostLink, hostAddr); err != nil && !isAlreadyExists(err) {
		return fmt.Errorf("failed to add host ip: %w", err)
	}

	if err := netlink.LinkSetUp(hostLink); err != nil {
		return fmt.Errorf("failed to set host veth up: %w", err)
	}

	// peer veth는 이미 target namespace 안으로 이동했으므로,
	// 이름 변경, IP 설정, link up 작업은 target namespace 안에서 수행해야 한다.
	if err := withNetns(targetNS, func() error {
		peerLink, err := netlink.LinkByName(tmpPeerName)
		if err != nil {
			return fmt.Errorf("failed to find peer veth in named netns: %w", err)
		}

		if err := netlink.LinkSetName(peerLink, req.PeerIfname); err != nil {
			return fmt.Errorf("failed to rename peer veth: %w", err)
		}

		peerLink, err = netlink.LinkByName(req.PeerIfname)
		if err != nil {
			return fmt.Errorf("failed to find renamed peer veth: %w", err)
		}

		peerAddr, err := netlink.ParseAddr(req.PeerIP)
		if err != nil {
			return fmt.Errorf("failed to parse peer_ip: %w", err)
		}

		if err := netlink.AddrAdd(peerLink, peerAddr); err != nil && !isAlreadyExists(err) {
			return fmt.Errorf("failed to add peer ip: %w", err)
		}

		if err := netlink.LinkSetUp(peerLink); err != nil {
			return fmt.Errorf("failed to set peer veth up: %w", err)
		}

		// checker는 namespace 내부의 lo interface가 UP 상태인지도 확인한다.
		loLink, err := netlink.LinkByName("lo")
		if err != nil {
			return fmt.Errorf("failed to find lo in named netns: %w", err)
		}

		if err := netlink.LinkSetUp(loLink); err != nil {
			return fmt.Errorf("failed to set lo up: %w", err)
		}

		return nil
	}); err != nil {
		return err
	}

	return nil
}

func execInNetns(name string, req execRequest) (int, int, error) {
	targetNS, err := netns.GetFromPath(netnsPath(name))
	if err != nil {
		return 0, 0, fmt.Errorf("failed to open named netns: %w", err)
	}
	defer targetNS.Close()

	// netns.Set()도 현재 스레드에 적용되므로 thread를 고정한다.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	originNS, err := netns.Get()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get origin netns: %w", err)
	}
	defer originNS.Close()

	if err := netns.Set(targetNS); err != nil {
		return 0, 0, fmt.Errorf("failed to enter named netns: %w", err)
	}

	defer func() {
		_ = netns.Set(originNS)
	}()

	// 현재 스레드가 target namespace 안에 있는 상태에서 프로세스를 시작한다.
	// 따라서 생성된 child process는 target namespace 안에서 실행된다.
	cmd := exec.Command(req.Path, req.Args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// echoserver는 checker가 접속할 때까지 계속 떠 있어야 하므로 Run()이 아니라 Start()를 사용한다.
	if err := cmd.Start(); err != nil {
		return 0, 0, fmt.Errorf("failed to start process in named netns: %w", err)
	}

	go func() {
		_ = cmd.Wait()
	}()

	return os.Getpid(), cmd.Process.Pid, nil
}

func withNetns(targetNS netns.NsHandle, fn func() error) error {
	// namespace 내부에서 netlink 작업이 필요할 때만 잠시 target namespace에 들어간다.
	// 작업이 끝나면 반드시 원래 namespace로 복귀한다.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	originNS, err := netns.Get()
	if err != nil {
		return fmt.Errorf("failed to get origin netns: %w", err)
	}
	defer originNS.Close()

	if err := netns.Set(targetNS); err != nil {
		return fmt.Errorf("failed to enter target netns: %w", err)
	}

	defer func() {
		_ = netns.Set(originNS)
	}()

	return fn()
}

func parseNetnsAction(path string) (string, string, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 3 || parts[0] != "netns" {
		return "", "", fmt.Errorf("invalid netns action path")
	}

	return parts[1], parts[2], nil
}

func netnsPath(name string) string {
	return filepath.Join(netnsDir, name)
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

	// Linux interface name 15자 제한으로 짧은 prefix와 hash를 사용한다.
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

	return strings.Contains(msg, "file exists") ||
		strings.Contains(msg, "object already exists")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}
