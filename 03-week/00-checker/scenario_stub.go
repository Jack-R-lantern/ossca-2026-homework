//go:build !linux

package main

import "errors"

func runScenario(cfg scenarioConfig) error {
	return errors.New("03-week checker scenario is only supported on linux")
}
