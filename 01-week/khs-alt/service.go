package main

import (
	"fmt"
	"os"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

func MakeProcess(path string, args []string) (*Output, error) {
	runtime.LockOSThread()
	// 고루틴이 하나의 os thread만 점유하도록 하기 위해 runtime.UnlockOSThread() 호출을 안함.
	// defer runtime.UnlockOSThread()

	argv := append([]string{path}, args...)

	parentPid := os.Getpid()
	err := unix.Unshare(unix.CLONE_NEWNET)
	if err != nil {
		return nil, err
	}
	childPid, err := syscall.ForkExec(path, argv, nil)
	if err != nil {
		return nil, err
	}
	tid := syscall.Gettid()
	fmt.Printf("thread id: %d\n", tid)

	output := Output{
		ParentPid: parentPid,
		ChildPid:  childPid,
	}
	return &output, nil
}
