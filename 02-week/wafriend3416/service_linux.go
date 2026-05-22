//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

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

func (linuxService) CreateVeth(string, vethRequest) (vethResponse, error) {
	return vethResponse{}, errors.New("veth API is not implemented yet")
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
