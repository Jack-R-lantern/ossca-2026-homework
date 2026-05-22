//go:build !linux

package main

import "errors"

type unsupportedService struct{}

func newService() service {
	return unsupportedService{}
}

func (unsupportedService) CreateNetns(string) (string, error) {
	return "", errors.New("network namespace operations require Linux")
}

func (unsupportedService) CreateVeth(string, vethRequest) (vethResponse, error) {
	return vethResponse{}, errors.New("network namespace operations require Linux")
}

func (unsupportedService) ExecInNetns(string, execRequest) (execResponse, error) {
	return execResponse{}, errors.New("network namespace operations require Linux")
}
