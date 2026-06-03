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

const netnsDir = "/var/run/netns" // WSL2/systemd 에선 /run/netns 로 연결됨

// ---------- 요청/응답 타입 ----------

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

// ---------- 라우팅 ----------

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /netns", handleNetns)
	mux.HandleFunc("POST /netns/{name}/veth", handleVeth)
	mux.HandleFunc("POST /netns/{name}/exec", handleExec)

	log.Println("API server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

// POST /netns
func handleNetns(w http.ResponseWriter, r *http.Request) {
	var req NetnsRequest
	json.NewDecoder(r.Body).Decode(&req)
	if err := validateName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	path, err := createNamedNetns(req.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, NetnsResponse{Name: req.Name, NetnsPath: path})
}

// POST /netns/{name}/veth
func handleVeth(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := validateName(name); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	nsPath, err := requireNetns(name)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	var req VethRequest
	json.NewDecoder(r.Body).Decode(&req)
	if req.HostIfname == "" || req.PeerIfname == "" || req.HostIP == "" || req.PeerIP == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("all veth fields are required"))
		return
	}

	if err := setupVeth(nsPath, req); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"name": name, "netns_path": nsPath,
		"host_ifname": req.HostIfname, "peer_ifname": req.PeerIfname,
		"host_ip": req.HostIP, "peer_ip": req.PeerIP,
	})
}

// POST /netns/{name}/exec
func handleExec(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := validateName(name); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	nsPath, err := requireNetns(name)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	var req ExecRequest
	json.NewDecoder(r.Body).Decode(&req)
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("path is required"))
		return
	}

	parentPID, childPID, err := execInNetns(nsPath, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, ExecResponse{Name: name, ParentPID: parentPID, ChildPID: childPID})
}

// ---------- 1) named network namespace 생성 ----------

func createNamedNetns(name string) (string, error) {
	if err := ensureNetnsDir(); err != nil {
		return "", err
	}
	path := filepath.Join(netnsDir, name)

	// 이미 nsfs 로 mount 되어 있으면 그대로 재사용
	if isNSFSMount(path) {
		return path, nil
	}

	unix.Unmount(path, unix.MNT_DETACH)
	os.Remove(path)

	// bind mount 대상이 될 빈 파일을 만든다.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL, 0o444)
	if err != nil {
		return "", fmt.Errorf("create mount point: %w", err)
	}
	f.Close()

	// 스레드를 고정해 새 netns 로 보낸 뒤, UnlockOSThread 하지 않음
	errc := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
			errc <- fmt.Errorf("unshare(CLONE_NEWNET): %w", err)
			return
		}
		// 이 스레드의 netns 파일을 bind mount → 스레드가 죽어도 namespace 유지
		src := fmt.Sprintf("/proc/%d/task/%d/ns/net", os.Getpid(), unix.Gettid())
		errc <- unix.Mount(src, path, "", unix.MS_BIND, "")
	}()
	if err := <-errc; err != nil {
		os.Remove(path)
		return "", fmt.Errorf("create netns: %w", err)
	}
	return path, nil
}

// ---------- 2) veth pair 생성 및 설정 ----------

func setupVeth(nsPath string, req VethRequest) error {
	hostAddr, err := netlink.ParseAddr(req.HostIP)
	if err != nil {
		return fmt.Errorf("parse host_ip: %w", err)
	}
	peerAddr, err := netlink.ParseAddr(req.PeerIP)
	if err != nil {
		return fmt.Errorf("parse peer_ip: %w", err)
	}

	ns, err := netns.GetFromPath(nsPath)
	if err != nil {
		return fmt.Errorf("open netns: %w", err)
	}
	defer ns.Close()

	// host netns 용 핸들과 named netns 용 핸들을 각각 만든다.
	hostH, err := netlink.NewHandle()
	if err != nil {
		return err
	}
	defer hostH.Close()
	nsH, err := netlink.NewHandleAt(ns)
	if err != nil {
		return err
	}
	defer nsH.Close()

	// 같은 이름이 남아 있으면 정리
	delLink(hostH, req.HostIfname)
	delLink(nsH, req.PeerIfname)

	// peer 는 임시 이름으로 생성
	tmp := tempPeerName(req.HostIfname)
	delLink(hostH, tmp)
	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: req.HostIfname},
		PeerName:  tmp,
	}
	if err := hostH.LinkAdd(veth); err != nil {
		return fmt.Errorf("add veth pair: %w", err)
	}

	// peer 를 named netns 안으로 이동한다.
	peer, err := hostH.LinkByName(tmp)
	if err != nil {
		return fmt.Errorf("find temp peer: %w", err)
	}
	if err := hostH.LinkSetNsFd(peer, int(ns)); err != nil {
		return fmt.Errorf("move peer into netns: %w", err)
	}

	// host 쪽: IP 설정 + UP
	host, err := hostH.LinkByName(req.HostIfname)
	if err != nil {
		return fmt.Errorf("find host veth: %w", err)
	}
	if err := hostH.AddrAdd(host, hostAddr); err != nil && !isExists(err) {
		return fmt.Errorf("set host ip: %w", err)
	}
	if err := hostH.LinkSetUp(host); err != nil {
		return fmt.Errorf("set host up: %w", err)
	}

	// named netns 쪽
	p, err := nsH.LinkByName(tmp)
	if err != nil {
		return fmt.Errorf("find peer in netns: %w", err)
	}
	if err := nsH.LinkSetDown(p); err != nil {
		return fmt.Errorf("set peer down: %w", err)
	}
	if err := nsH.LinkSetName(p, req.PeerIfname); err != nil {
		return fmt.Errorf("rename peer: %w", err)
	}
	p, err = nsH.LinkByName(req.PeerIfname)
	if err != nil {
		return fmt.Errorf("find renamed peer: %w", err)
	}
	if err := nsH.AddrAdd(p, peerAddr); err != nil && !isExists(err) {
		return fmt.Errorf("set peer ip: %w", err)
	}
	if err := nsH.LinkSetUp(p); err != nil {
		return fmt.Errorf("set peer up: %w", err)
	}

	// named netns 내부 loopback UP
	if lo, err := nsH.LinkByName("lo"); err == nil {
		nsH.LinkSetUp(lo)
	}
	return nil
}

// ---------- 3) namespace 안에서 프로세스 실행 ----------

func execInNetns(nsPath string, req ExecRequest) (parentPID, childPID int, err error) {
	type result struct {
		cmd *exec.Cmd
		err error
	}
	ch := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		ns, err := netns.GetFromPath(nsPath)
		if err != nil {
			ch <- result{nil, err}
			return
		}
		if err := netns.Set(ns); err != nil { // 이 스레드를 target netns 로 진입
			ch <- result{nil, err}
			return
		}
		cmd := exec.Command(req.Path, req.Args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		err = cmd.Start() // 이 스레드가 target netns 이므로 자식도 그 안에서 실행됨
		ch <- result{cmd, err}
	}()

	r := <-ch
	if r.err != nil {
		return 0, 0, fmt.Errorf("exec in netns: %w", r.err)
	}
	go r.cmd.Wait() // 좀비 방지
	return os.Getpid(), r.cmd.Process.Pid, nil
}

// ---------- 헬퍼 ----------

// netns 디렉터리를 만들고 shared mount 로 만든다
func ensureNetnsDir() error {
	if err := os.MkdirAll(netnsDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", netnsDir, err)
	}
	if unix.Mount("", netnsDir, "none", unix.MS_SHARED|unix.MS_REC, "") != nil {
		unix.Mount(netnsDir, netnsDir, "none", unix.MS_BIND|unix.MS_REC, "")
		unix.Mount("", netnsDir, "none", unix.MS_SHARED|unix.MS_REC, "")
	}
	return nil
}

func delLink(h *netlink.Handle, name string) {
	if l, err := h.LinkByName(name); err == nil {
		_ = h.LinkDel(l)
		time.Sleep(50 * time.Millisecond) // 커널이 링크를 정리할 시간을 준다
	}
}

// host 인터페이스 이름으로부터 결정적인(매번 같은) 임시 peer 이름을 만든다.
func tempPeerName(hostIfname string) string {
	h := fnv.New32a()
	h.Write([]byte(hostIfname))
	return fmt.Sprintf("tmp%x", h.Sum32())
}

func isNSFSMount(path string) bool {
	var st unix.Statfs_t
	if unix.Statfs(path, &st) != nil {
		return false
	}
	return st.Type == unix.NSFS_MAGIC
}

func isExists(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "exists")
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

// namespace 가 실제로 존재하는지 확인하고 경로를 돌려준다.
func requireNetns(name string) (string, error) {
	path := filepath.Join(netnsDir, name)
	if !isNSFSMount(path) {
		return "", fmt.Errorf("network namespace %q not found (create it first)", name)
	}
	return path, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	log.Printf("request failed: status=%d error=%v", status, err)
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
