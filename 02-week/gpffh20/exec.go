//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"
)

func execInNetns(nsName string, req execReq) (int, int, error) {
	nsPath := filepath.Join(netnsDir, nsName)

	type result struct {
		childPID int
		err      error
	}
	ch := make(chan result, 1)

	go func() {
		runtime.LockOSThread()

		origFd, err := openThreadNetNS()
		if err != nil {
			runtime.UnlockOSThread()
			ch <- result{err: fmt.Errorf("open original netns: %w", err)}
			return
		}
		defer unix.Close(origFd)

		targetFd, err := unix.Open(nsPath, unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if err != nil {
			runtime.UnlockOSThread()
			ch <- result{err: fmt.Errorf("open target netns %s: %w", nsPath, err)}
			return
		}
		defer unix.Close(targetFd)

		if err := unix.Setns(targetFd, unix.CLONE_NEWNET); err != nil {
			runtime.UnlockOSThread()
			ch <- result{err: fmt.Errorf("setns(%s): %w", nsPath, err)}
			return
		}

		var args []string
		for _, a := range req.Args {
			if a != "" {
				args = append(args, a)
			}
		}

		cmd := exec.Command(req.Path, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		startErr := cmd.Start()

		if restoreErr := unix.Setns(origFd, unix.CLONE_NEWNET); restoreErr != nil {
			if startErr == nil {
				go func() { _ = cmd.Wait() }()
				ch <- result{childPID: cmd.Process.Pid, err: fmt.Errorf("restore host netns: %w", restoreErr)}
			} else {
				ch <- result{err: startErr}
			}
			runtime.Goexit()
			return
		}

		runtime.UnlockOSThread()

		if startErr != nil {
			ch <- result{err: fmt.Errorf("exec %s in %s: %w", req.Path, nsPath, startErr)}
			return
		}

		go func() { _ = cmd.Wait() }()
		ch <- result{childPID: cmd.Process.Pid}
	}()

	res := <-ch
	if res.err != nil {
		return 0, 0, res.err
	}
	return os.Getpid(), res.childPID, nil
}
