package main

import (
	"fmt"
	"os"
	"runtime"
	"syscall"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func MakeProcess(name string) (*Output, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	targetPath := fmt.Sprintf("/var/run/netns/%s", name)

	// host의 NS fd
	ParentNetNSFd, err := os.Open("/proc/self/ns/net")
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	defer ParentNetNSFd.Close()

	err = unix.Unshare(unix.CLONE_NEWNET)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	// /proc/<pid> 가 아니라 thread-self 여야 함 (ns 는 thread 단위)
	sourcePath := "/proc/thread-self/ns/net"

	// bind mount target 파일 준비
	if err := os.MkdirAll("/var/run/netns", 0755); err != nil {
		return nil, err
	}
	f, err := os.Create(targetPath)
	if err != nil {
		return nil, err
	}
	f.Close()

	err = unix.Mount(sourcePath, targetPath, "", unix.MS_BIND, "")
	if err != nil {
		return nil, fmt.Errorf("bind mount %s -> %s: %w", sourcePath, targetPath, err)
	}

	err = unix.Setns(int(ParentNetNSFd.Fd()), unix.CLONE_NEWNET) // unix.Setns(int(f.Fd()), 0)도 가능, 안전을 위해 동일하게 명시해주는 것이 좋음
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	output := Output{
		Name:      name,
		NetNSPath: targetPath,
	}
	return &output, nil
}

func AddVeth(name, hostIf, peerIf, hostCIDR, peerCIDR string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	targetPath := fmt.Sprintf("/var/run/netns/%s", name)

	ParentNetNSFd, err := os.Open("/proc/self/ns/net")
	if err != nil {
		return fmt.Errorf("open host ns: %w", err)
	}
	defer ParentNetNSFd.Close()

	TargetNetNSFd, err := os.Open(targetPath)
	if err != nil {
		return fmt.Errorf("open target ns %s: %w", targetPath, err)
	}
	defer TargetNetNSFd.Close()

	// create veth (peer 는 처음부터 target ns 안에 생성 — host의 eth0 와 충돌 방지)
	veth := &netlink.Veth{
		LinkAttrs:     netlink.LinkAttrs{Name: hostIf},
		PeerName:      peerIf,
		PeerNamespace: netlink.NsFd(int(TargetNetNSFd.Fd())),
	}
	if err := netlink.LinkAdd(veth); err != nil {
		return fmt.Errorf("link add veth %s<->%s: %w", hostIf, peerIf, err)
	}

	// set ip
	host, err := netlink.LinkByName(hostIf)
	if err != nil {
		return fmt.Errorf("lookup host %s: %w", hostIf, err)
	}
	hAddr, err := netlink.ParseAddr(hostCIDR)
	if err != nil {
		return fmt.Errorf("parse host cidr %s: %w", hostCIDR, err)
	}
	if err := netlink.AddrAdd(host, hAddr); err != nil {
		return fmt.Errorf("addr add host: %w", err)
	}
	if err := netlink.LinkSetUp(host); err != nil {
		return fmt.Errorf("link up host: %w", err)
	}

	// move target ns and set peer
	if err := unix.Setns(int(TargetNetNSFd.Fd()), unix.CLONE_NEWNET); err != nil {
		return fmt.Errorf("setns target: %w", err)
	}
	defer unix.Setns(int(ParentNetNSFd.Fd()), unix.CLONE_NEWNET)

	peerLink, err := netlink.LinkByName(peerIf)
	if err != nil {
		return fmt.Errorf("lookup peer in ns: %w", err)
	}
	pAddr, err := netlink.ParseAddr(peerCIDR)
	if err != nil {
		return fmt.Errorf("parse peer cidr %s: %w", peerCIDR, err)
	}
	if err := netlink.AddrAdd(peerLink, pAddr); err != nil {
		return fmt.Errorf("addr add peer: %w", err)
	}
	if err := netlink.LinkSetUp(peerLink); err != nil {
		return fmt.Errorf("link up peer: %w", err)
	}

	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return fmt.Errorf("lookup lo: %w", err)
	}
	if err := netlink.LinkSetUp(lo); err != nil {
		return fmt.Errorf("link up lo: %w", err)
	}
	return nil
}

func ExecInNetns(name, path string, args []string) (int, int, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	parentPid := os.Getpid()
	targetPath := fmt.Sprintf("/var/run/netns/%s", name)

	// host의 NS fd
	ParentNetNSFd, err := os.Open("/proc/self/ns/net")
	if err != nil {
		return 0, 0, fmt.Errorf("open host ns: %w", err)
	}
	defer ParentNetNSFd.Close()

	// target NS fd
	TargetNetNSFd, err := os.Open(targetPath)
	if err != nil {
		return 0, 0, fmt.Errorf("open target ns %s: %w", targetPath, err)
	}
	defer TargetNetNSFd.Close()

	// move target NS
	if err := unix.Setns(int(TargetNetNSFd.Fd()), unix.CLONE_NEWNET); err != nil {
		return 0, 0, fmt.Errorf("setns target: %w", err)
	}

	defer unix.Setns(int(ParentNetNSFd.Fd()), unix.CLONE_NEWNET)

	// argv[0]에 program 이름 포함 (Unix convention)
	argv := append([]string{path}, args...)

	// fork + exec
	childPid, err := syscall.ForkExec(path, argv, &syscall.ProcAttr{
		Env:   os.Environ(),
		Files: []uintptr{0, 1, 2}, // stdin/stdout/stderr 상속
	})
	if err != nil {
		return 0, 0, fmt.Errorf("forkexec %s: %w", path, err)
	}

	// zombie 방지
	go func() {
		var ws syscall.WaitStatus
		_, _ = syscall.Wait4(childPid, &ws, 0, nil)
	}()

	return parentPid, childPid, nil
}
