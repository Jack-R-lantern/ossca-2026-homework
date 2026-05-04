package main

import (
	"os"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

func MakeProcess(path string, args []string) (*Output, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	argv := append([]string{path}, args...)
	parentPid := os.Getpid()
	ParentNetNSFd, err := os.Open("/proc/self/ns/net")
	if err != nil {
		return nil, err
	}
	defer ParentNetNSFd.Close()

	err = unix.Unshare(unix.CLONE_NEWNET)
	if err != nil {
		return nil, err
	}
	childPid, err := syscall.ForkExec(path, argv, nil)
	if err != nil {
		return nil, err
	}
	err = unix.Setns(int(ParentNetNSFd.Fd()), unix.CLONE_NEWNET) // unix.Setns(int(f.Fd()), 0)도 가능, 안전을 위해 동일하게 명시해주는 것이 좋음
	if err != nil {
		return nil, err
	}
	output := Output{
		ParentPid: parentPid,
		ChildPid:  childPid,
	}
	return &output, nil
}
