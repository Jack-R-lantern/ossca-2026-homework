//go:build linux

package main

import (
	"encoding/binary"
	"fmt"
	"net/netip"
)

func parseIPv4(ip string) (uint32, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return 0, fmt.Errorf("parse IPv4 address %q: %w", ip, err)
	}

	addr = addr.Unmap()
	if !addr.Is4() {
		return 0, fmt.Errorf("address %q is not IPv4", ip)
	}

	raw := addr.As4()
	return binary.BigEndian.Uint32(raw[:]), nil
}
