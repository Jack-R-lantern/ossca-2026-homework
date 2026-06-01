//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"
)

func createNamedNetns(name string) (string, error) {
	path := filepath.Join(netnsDir, name)

	if err := os.MkdirAll(netnsDir, 0755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", netnsDir, err)
	}

	if _, err := os.Stat(path); err == nil {
		if isNSFSMount(path) {
			return path, nil
		}
		return "", fmt.Errorf("%s exists but is not an nsfs bind mount", path)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL, 0444)
	if err != nil {
		return "", fmt.Errorf("create mount point %s: %w", path, err)
	}
	f.Close()

	mounted := false
	defer func() {
		if !mounted {
			os.Remove(path)
		}
	}()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origFd, err := openThreadNetNS()
	if err != nil {
		return "", fmt.Errorf("open original netns: %w", err)
	}
	defer unix.Close(origFd)

	if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
		return "", fmt.Errorf("unshare(CLONE_NEWNET): %w", err)
	}

	defer func() {
		unix.Setns(origFd, unix.CLONE_NEWNET)
	}()

	tid := unix.Gettid()
	procNS := fmt.Sprintf("/proc/self/task/%d/ns/net", tid)
	if err := unix.Mount(procNS, path, "", unix.MS_BIND, ""); err != nil {
		return "", fmt.Errorf("bind mount %s → %s: %w", procNS, path, err)
	}

	mounted = true
	return path, nil
}

func withNetNS(nsPath string, fn func() error) (retErr error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origFd, err := openThreadNetNS()
	if err != nil {
		return fmt.Errorf("open original netns: %w", err)
	}
	defer unix.Close(origFd)

	targetFd, err := unix.Open(nsPath, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open target netns %s: %w", nsPath, err)
	}
	defer unix.Close(targetFd)

	if err := unix.Setns(targetFd, unix.CLONE_NEWNET); err != nil {
		return fmt.Errorf("setns(%s): %w", nsPath, err)
	}

	fnErr := fn()

	if restoreErr := unix.Setns(origFd, unix.CLONE_NEWNET); restoreErr != nil {
		if fnErr == nil {
			return fmt.Errorf("restore original netns: %w", restoreErr)
		}
	}

	return fnErr
}

func openThreadNetNS() (int, error) {
	path := fmt.Sprintf("/proc/self/task/%d/ns/net", unix.Gettid())
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("open %s: %w", path, err)
	}
	return fd, nil
}

func isNSFSMount(path string) bool {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return false
	}
	return st.Type == unix.NSFS_MAGIC
}
