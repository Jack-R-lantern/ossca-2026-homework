//go:build linux

package main

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
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

func (linuxService) CreateNetns(name string) (path string, err error) {
	path = netnsPath(name)

	if err := os.MkdirAll(netnsDir, 0755); err != nil {
		return "", fmt.Errorf("create netns dir: %w", err)
	}

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
		if err != nil && createdMountPoint {
			_ = os.Remove(path)
		}
	}()

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
		if restoreErr := setnsFile(originNS); err == nil && restoreErr != nil {
			err = fmt.Errorf("restore original netns: %w", restoreErr)
		}
	}()

	threadNSPath := fmt.Sprintf("/proc/self/task/%d/ns/net", unix.Gettid())
	if err := unix.Mount(threadNSPath, path, "", unix.MS_BIND, ""); err != nil {
		return "", fmt.Errorf("bind mount named netns: %w", err)
	}

	createdMountPoint = false
	return path, nil
}

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

	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: req.HostIfname},
		PeerName:  tmpPeerName,
	}
	if err := netlink.LinkAdd(veth); err != nil {
		return vethResponse{}, fmt.Errorf("add veth pair: %w", err)
	}

	cleanup := true
	defer func() {
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

	if err := netlink.LinkSetNsFd(peerLink, int(targetNS.Fd())); err != nil {
		return vethResponse{}, fmt.Errorf("move peer veth to named netns: %w", err)
	}

	if err := netlink.AddrAdd(hostLink, hostAddr); err != nil {
		return vethResponse{}, fmt.Errorf("add host ip: %w", err)
	}

	if err := netlink.LinkSetUp(hostLink); err != nil {
		return vethResponse{}, fmt.Errorf("set host veth up: %w", err)
	}

	if err := withNamedNetNS(path, func() error {
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

func (linuxService) ExecInNetns(string, execRequest) (execResponse, error) {
	return execResponse{}, errors.New("exec API is not implemented yet")
}

func netnsPath(name string) string {
	return filepath.Join(netnsDir, name)
}

func openCurrentThreadNetNS() (*os.File, error) {
	return os.Open(fmt.Sprintf("/proc/self/task/%d/ns/net", unix.Gettid()))
}

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
