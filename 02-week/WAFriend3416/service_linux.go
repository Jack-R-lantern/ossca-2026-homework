//go:build linux

package main

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const netnsDir = "/var/run/netns"

type linuxService struct{}

func newService() service {
	return linuxService{}
}

// CreateNetns는 1주차의 unshare 흐름을 named namespace 생성으로 확장한다.
// 새 network namespace를 만든 뒤 /var/run/netns/{name}에 bind mount해서 이름으로 다시 열 수 있게 한다.
func (linuxService) CreateNetns(name string) (path string, err error) {
	path = netnsPath(name)

	if err := os.MkdirAll(netnsDir, 0755); err != nil {
		return "", fmt.Errorf("create netns dir: %w", err)
	}

	// 같은 이름의 namespace가 이미 nsfs mount라면 반복 호출을 성공으로 본다.
	if _, err := os.Stat(path); err == nil {
		if isNSFSMount(path) {
			return path, nil
		}

		return "", fmt.Errorf("netns path exists but is not an nsfs mount: %s", path)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat netns path: %w", err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL, 0444)
	if err != nil {
		return "", fmt.Errorf("create netns mount point: %w", err)
	}
	_ = file.Close()

	createdMountPoint := true
	defer func() {
		// mount 전에 실패했다면 빈 mount point 파일만 정리한다.
		if err != nil && createdMountPoint {
			_ = os.Remove(path)
		}
	}()

	// unshare와 setns는 현재 OS thread에 적용된다.
	// Go runtime이 goroutine을 다른 thread로 옮기지 못하도록 고정한다.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	originNS, err := openCurrentThreadNetNS()
	if err != nil {
		return "", fmt.Errorf("open original netns: %w", err)
	}
	defer originNS.Close()

	if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
		return "", fmt.Errorf("unshare netns: %w", err)
	}

	defer func() {
		// 부모 HTTP 서버가 host namespace에 남아 있어야 하므로 항상 원래 netns로 복귀한다.
		if restoreErr := setnsFile(originNS); err == nil && restoreErr != nil {
			err = fmt.Errorf("restore original netns: %w", restoreErr)
		}
	}()

	// 현재 thread는 새 netns에 들어와 있으므로 이 경로가 새 namespace를 가리킨다.
	// bind mount 결과가 checker에서 nsfs mount로 보인다.
	threadNSPath := fmt.Sprintf("/proc/self/task/%d/ns/net", unix.Gettid())
	if err := unix.Mount(threadNSPath, path, "", unix.MS_BIND, ""); err != nil {
		return "", fmt.Errorf("bind mount named netns: %w", err)
	}

	createdMountPoint = false
	return path, nil
}

// CreateVeth는 host namespace에 veth pair를 만들고 peer 쪽만 named namespace로 이동한다.
// 모든 설정은 netlink socket으로 수행하며 ip 명령어는 호출하지 않는다.
func (linuxService) CreateVeth(name string, req vethRequest) (vethResponse, error) {
	if err := validateIfName(req.HostIfname); err != nil {
		return vethResponse{}, fmt.Errorf("host_ifname: %w", err)
	}

	if err := validateIfName(req.PeerIfname); err != nil {
		return vethResponse{}, fmt.Errorf("peer_ifname: %w", err)
	}

	hostAddr, err := netlink.ParseAddr(req.HostIP)
	if err != nil {
		return vethResponse{}, fmt.Errorf("parse host_ip: %w", err)
	}

	peerAddr, err := netlink.ParseAddr(req.PeerIP)
	if err != nil {
		return vethResponse{}, fmt.Errorf("parse peer_ip: %w", err)
	}

	path := netnsPath(name)
	// LinkSetNsFd는 대상 namespace의 fd가 필요하다.
	targetNS, err := os.Open(path)
	if err != nil {
		return vethResponse{}, fmt.Errorf("open named netns: %w", err)
	}
	defer targetNS.Close()

	if exists, err := linkExists(req.HostIfname); err != nil {
		return vethResponse{}, fmt.Errorf("check host link: %w", err)
	} else if exists {
		return vethResponse{}, fmt.Errorf("host interface already exists: %s", req.HostIfname)
	}

	tmpPeerName := tempPeerName(req.HostIfname)
	if exists, err := linkExists(tmpPeerName); err != nil {
		return vethResponse{}, fmt.Errorf("check temporary peer link: %w", err)
	} else if exists {
		return vethResponse{}, fmt.Errorf("temporary peer interface already exists: %s", tmpPeerName)
	}

	// peer_ifname은 namespace 내부에서만 보이는 이름이므로 잠시 target namespace에 들어가 확인한다.
	if err := withNamedNetNS(path, func() error {
		exists, err := linkExists(req.PeerIfname)
		if err != nil {
			return fmt.Errorf("check namespace peer link: %w", err)
		}

		if exists {
			return fmt.Errorf("peer interface already exists in namespace: %s", req.PeerIfname)
		}

		return nil
	}); err != nil {
		return vethResponse{}, err
	}

	// peer는 아직 host namespace에 있으므로 임시 이름으로 만든 뒤 나중에 namespace 안에서 rename한다.
	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: req.HostIfname},
		PeerName:  tmpPeerName,
	}
	if err := netlink.LinkAdd(veth); err != nil {
		return vethResponse{}, fmt.Errorf("add veth pair: %w", err)
	}

	cleanup := true
	defer func() {
		// 중간 실패 시 host 쪽 veth를 삭제하면 peer도 같이 정리된다.
		if cleanup {
			cleanupHostLink(req.HostIfname)
		}
	}()

	hostLink, err := netlink.LinkByName(req.HostIfname)
	if err != nil {
		return vethResponse{}, fmt.Errorf("lookup host veth: %w", err)
	}

	peerLink, err := netlink.LinkByName(tmpPeerName)
	if err != nil {
		return vethResponse{}, fmt.Errorf("lookup temporary peer veth: %w", err)
	}

	// peer link만 named namespace로 이동한다. hostLink는 host namespace에 남는다.
	if err := netlink.LinkSetNsFd(peerLink, int(targetNS.Fd())); err != nil {
		return vethResponse{}, fmt.Errorf("move peer veth to named netns: %w", err)
	}

	// host 쪽 IP와 UP 상태는 host namespace에서 바로 설정한다.
	if err := netlink.AddrAdd(hostLink, hostAddr); err != nil {
		return vethResponse{}, fmt.Errorf("add host ip: %w", err)
	}

	if err := netlink.LinkSetUp(hostLink); err != nil {
		return vethResponse{}, fmt.Errorf("set host veth up: %w", err)
	}

	if err := withNamedNetNS(path, func() error {
		// 여기부터는 named namespace 내부에서 peer veth와 lo를 설정한다.
		peerLink, err := netlink.LinkByName(tmpPeerName)
		if err != nil {
			return fmt.Errorf("lookup peer veth in named netns: %w", err)
		}

		if err := netlink.LinkSetName(peerLink, req.PeerIfname); err != nil {
			return fmt.Errorf("rename peer veth: %w", err)
		}

		peerLink, err = netlink.LinkByName(req.PeerIfname)
		if err != nil {
			return fmt.Errorf("lookup renamed peer veth: %w", err)
		}

		if err := netlink.AddrAdd(peerLink, peerAddr); err != nil {
			return fmt.Errorf("add peer ip: %w", err)
		}

		if err := netlink.LinkSetUp(peerLink); err != nil {
			return fmt.Errorf("set peer veth up: %w", err)
		}

		// checker는 namespace 내부 loopback도 UP인지 확인한다.
		loLink, err := netlink.LinkByName("lo")
		if err != nil {
			return fmt.Errorf("lookup loopback: %w", err)
		}

		if err := netlink.LinkSetUp(loLink); err != nil {
			return fmt.Errorf("set loopback up: %w", err)
		}

		return nil
	}); err != nil {
		return vethResponse{}, err
	}

	cleanup = false
	return vethResponse{
		Name:       name,
		HostIfname: req.HostIfname,
		PeerIfname: req.PeerIfname,
		HostIP:     req.HostIP,
		PeerIP:     req.PeerIP,
		NetnsPath:  path,
	}, nil
}

// ExecInNetns는 named namespace로 들어간 thread에서 프로세스를 Start한다.
// Start된 child는 그 namespace를 상속하고, 부모 thread는 즉시 host namespace로 돌아온다.
func (linuxService) ExecInNetns(name string, req execRequest) (execResponse, error) {
	var childPID int

	if err := withNamedNetNS(netnsPath(name), func() error {
		cmd := exec.Command(req.Path, req.Args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start process in named netns: %w", err)
		}

		childPID = cmd.Process.Pid
		// API 응답 후에도 child를 수거해 zombie가 남지 않게 한다.
		go func() {
			_ = cmd.Wait()
		}()

		return nil
	}); err != nil {
		return execResponse{}, err
	}

	return execResponse{
		Name:      name,
		ParentPID: currentPID(),
		ChildPID:  childPID,
	}, nil
}

func netnsPath(name string) string {
	return filepath.Join(netnsDir, name)
}

func openCurrentThreadNetNS() (*os.File, error) {
	return os.Open(fmt.Sprintf("/proc/self/task/%d/ns/net", unix.Gettid()))
}

// setnsFile은 1주차 setns syscall wrapper와 같은 역할을 한다.
// 여기서는 x/sys/unix의 Setns를 사용해 현재 thread의 network namespace를 바꾼다.
func setnsFile(file *os.File) error {
	return unix.Setns(int(file.Fd()), unix.CLONE_NEWNET)
}

func isNSFSMount(path string) bool {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return false
	}

	return stat.Type == unix.NSFS_MAGIC
}

// withNamedNetNS는 특정 namespace 안에서만 실행해야 하는 netlink/exec 작업을 감싼다.
// 함수가 끝나면 성공/실패와 관계없이 원래 namespace로 복귀를 시도한다.
func withNamedNetNS(path string, fn func() error) error {
	targetNS, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open target netns: %w", err)
	}
	defer targetNS.Close()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	originNS, err := openCurrentThreadNetNS()
	if err != nil {
		return fmt.Errorf("open original netns: %w", err)
	}
	defer originNS.Close()

	if err := setnsFile(targetNS); err != nil {
		return fmt.Errorf("enter target netns: %w", err)
	}

	fnErr := fn()
	restoreErr := setnsFile(originNS)
	if fnErr != nil {
		return fnErr
	}

	if restoreErr != nil {
		return fmt.Errorf("restore original netns: %w", restoreErr)
	}

	return nil
}

// Linux interface name은 IFNAMSIZ 제한 때문에 실제 이름은 15자 이하여야 한다.
func validateIfName(name string) error {
	if name == "" {
		return errors.New("interface name is required")
	}

	if len(name) > 15 {
		return errors.New("interface name must be 15 characters or less")
	}

	if strings.Contains(name, "/") || strings.Contains(name, "\x00") {
		return errors.New("interface name contains invalid characters")
	}

	return nil
}

// netlink는 존재하지 않는 interface를 조회할 때 환경별로 다른 메시지를 돌려줄 수 있다.
func linkExists(name string) (bool, error) {
	if _, err := netlink.LinkByName(name); err != nil {
		if isLinkNotFound(err) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

func cleanupHostLink(name string) {
	link, err := netlink.LinkByName(name)
	if err == nil {
		_ = netlink.LinkDel(link)
	}
}

// tempPeerName은 host interface 이름에서 안정적인 임시 peer 이름을 만든다.
// veth 생성 직후 peer가 host namespace에 잠깐 존재할 때 이 이름으로 찾는다.
func tempPeerName(hostIfname string) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(hostIfname))

	return fmt.Sprintf("tmp%x", hash.Sum32())
}

func isLinkNotFound(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") ||
		strings.Contains(msg, "no such network interface") ||
		strings.Contains(msg, "link not found")
}
