//go:build !linux

package main

import "errors"

type unsupportedService struct{}

func newService() service {
	// macOS 등 Linux가 아닌 환경에서는 빌드만 가능하게 하고 실제 XDP 작업은 거부한다.
	return unsupportedService{}
}

func (unsupportedService) Attach(string) (attachResponse, error) {
	return attachResponse{}, errors.New("xdp operations require Linux")
}

func (unsupportedService) Block(string, string) (blockResponse, error) {
	return blockResponse{}, errors.New("xdp operations require Linux")
}

func (unsupportedService) Clear(string) (clearResponse, error) {
	return clearResponse{}, errors.New("xdp operations require Linux")
}

func (unsupportedService) Close() error {
	return nil
}
