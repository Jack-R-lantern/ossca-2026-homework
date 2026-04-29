package main

import (
	"fmt"
	"syscall"
)

func main() {
	return
}

func createNSContainer(path string, args []string) (int, error) {
	if path != "/bin/sleep" {
		return -1, fmt.Errorf("path must be /bin/sleep")
	}
	pid, err := syscall.ForkExec(path, args, &syscall.ProcAttr{
		Sys: &syscall.SysProcAttr{
			Cloneflags: syscall.CLONE_NEWNS,
		},
	})
	if err != nil {
		return -1, err
	}
	return pid, nil
}
