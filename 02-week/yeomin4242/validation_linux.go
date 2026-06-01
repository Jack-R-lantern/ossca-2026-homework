//go:build linux

package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vishvananda/netlink"
)

func validateVethRequest(req createVethRequest) error {
	if err := validateIfName("host_ifname", req.HostIfname); err != nil {
		return err
	}

	if err := validateIfName("peer_ifname", req.PeerIfname); err != nil {
		return err
	}

	if _, err := netlink.ParseAddr(req.HostIP); err != nil {
		return fmt.Errorf("host_ip must be CIDR notation: %w", err)
	}

	if _, err := netlink.ParseAddr(req.PeerIP); err != nil {
		return fmt.Errorf("peer_ip must be CIDR notation: %w", err)
	}

	return nil
}

func validateIfName(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}

	if value == "lo" {
		return fmt.Errorf("%s must not be lo", field)
	}

	if len(value) > 15 {
		return fmt.Errorf("%s must be 15 bytes or shorter", field)
	}

	if strings.Contains(value, "/") || strings.Contains(value, "\x00") {
		return fmt.Errorf("%s contains invalid characters", field)
	}

	return nil
}

func validateName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}

	if strings.Contains(name, "/") || strings.Contains(name, "\x00") || name == "." || name == ".." {
		return fmt.Errorf("invalid namespace name %q", name)
	}

	return nil
}

func parseNetnsAction(path string) (string, string, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 3 || parts[0] != "netns" {
		return "", "", errors.New("invalid netns action path")
	}

	return parts[1], parts[2], nil
}
