package main

import (
	"errors"
	"os"
	"strings"
)

type Input struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

type Output struct {
	ParentPid int `json:"parent_pid"`
	ChildPid  int `json:"child_pid"`
}

func (i *Input) Validate() error {
	if i.Path == "" {
		return errors.New("path is required")
	}
	if len(i.Args) == 0 {
		return errors.New("args is required")
	}

	if !strings.HasPrefix(i.Path, "/") {
		return errors.New("path must be absolute")
	}

	if _, err := os.Stat(i.Path); err != nil {
		return errors.New("path does not exist")
	}

	return nil
}
