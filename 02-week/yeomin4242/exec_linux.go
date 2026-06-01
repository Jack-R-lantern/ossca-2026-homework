//go:build linux

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"

	"github.com/vishvananda/netns"
)

func execInNetns(name string, req execRequest) (int, int, error) {
	targetNS, err := netns.GetFromPath(netnsPath(name))
	if err != nil {
		return 0, 0, fmt.Errorf("open named network namespace %s: %w", netnsPath(name), err)
	}
	defer targetNS.Close()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	originNS, err := netns.Get()
	if err != nil {
		return 0, 0, fmt.Errorf("get current network namespace: %w", err)
	}
	defer originNS.Close()

	if err := netns.Set(targetNS); err != nil {
		return 0, 0, fmt.Errorf("enter named network namespace %s: %w", netnsPath(name), err)
	}

	restoreOrigin := true
	defer func() {
		if restoreOrigin {
			if err := netns.Set(originNS); err != nil {
				log.Printf("failed to restore original network namespace: %v", err)
			}
		}
	}()

	cmd := exec.Command(req.Path, req.Args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return 0, 0, fmt.Errorf("start %s in %s: %w", req.Path, netnsPath(name), err)
	}

	if err := netns.Set(originNS); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return 0, 0, fmt.Errorf("restore original network namespace: %w", err)
	}

	restoreOrigin = false

	go func() {
		_ = cmd.Wait()
	}()

	return os.Getpid(), cmd.Process.Pid, nil
}
