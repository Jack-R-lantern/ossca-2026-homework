//go:build linux

package main

import "errors"

type linuxService struct{}

func newService() service {
	return linuxService{}
}

func (linuxService) CreateNetns(string) (string, error) {
	return "", errors.New("netns API is not implemented yet")
}

func (linuxService) CreateVeth(string, vethRequest) (vethResponse, error) {
	return vethResponse{}, errors.New("veth API is not implemented yet")
}

func (linuxService) ExecInNetns(string, execRequest) (execResponse, error) {
	return execResponse{}, errors.New("exec API is not implemented yet")
}
