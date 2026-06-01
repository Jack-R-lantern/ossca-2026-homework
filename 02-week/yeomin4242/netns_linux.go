//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

func createNamedNetns(name string) (string, error) {
	path := netnsPath(name)

	if err := os.MkdirAll(netnsDir, 0755); err != nil {
		return "", fmt.Errorf("create netns directory %s: %w", netnsDir, err)
	}

	if _, err := os.Stat(path); err == nil {
		if isNSFSMount(path) {
			return path, nil
		}

		return "", fmt.Errorf("%s already exists but is not an nsfs mount", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat netns mount point %s: %w", path, err)
	}

	mountPoint, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL, 0444)
	if err != nil {
		return "", fmt.Errorf("create netns mount point %s: %w", path, err)
	}
	_ = mountPoint.Close()

	created := false
	mounted := false
	defer func() {
		if !created {
			if mounted {
				_ = unix.Unmount(path, unix.MNT_DETACH)
			}
			_ = os.Remove(path)
		}
	}()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	originNS, err := netns.Get()
	if err != nil {
		return "", fmt.Errorf("get current network namespace: %w", err)
	}
	defer originNS.Close()

	newNS, err := netns.New()
	if err != nil {
		return "", fmt.Errorf("create network namespace: %w", err)
	}
	defer newNS.Close()

	restoreOrigin := true
	defer func() {
		if restoreOrigin {
			_ = netns.Set(originNS)
		}
	}()

	threadNetNS := fmt.Sprintf("/proc/self/task/%d/ns/net", unix.Gettid())
	if err := unix.Mount(threadNetNS, path, "", unix.MS_BIND, ""); err != nil {
		return "", fmt.Errorf("bind mount %s to %s: %w", threadNetNS, path, err)
	}
	mounted = true

	if err := netns.Set(originNS); err != nil {
		return "", fmt.Errorf("restore original network namespace: %w", err)
	}

	restoreOrigin = false
	created = true
	return path, nil
}

func netnsPath(name string) string {
	return filepath.Join(netnsDir, name)
}

func isNSFSMount(path string) bool {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return false
	}

	return stat.Type == unix.NSFS_MAGIC
}
