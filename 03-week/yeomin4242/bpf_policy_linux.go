//go:build linux

package main

import "fmt"

func (m *bpfManager) BlockIP(ifName, ip string) error {
	ipv4, err := parseIPv4(ip)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	target, ok := m.attached[ifName]
	if !ok {
		return fmt.Errorf("XDP program is not attached to %q", ifName)
	}

	if err := target.verifyAttached(ifName); err != nil {
		return err
	}

	value := uint8(1)
	if err := target.blocked.Put(ipv4, value); err != nil {
		return fmt.Errorf("update blocked IP map for %q: %w", ifName, err)
	}

	return nil
}

func (m *bpfManager) Clear(ifName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	target, ok := m.attached[ifName]
	if !ok {
		return fmt.Errorf("XDP program is not attached to %q", ifName)
	}

	if err := target.verifyAttached(ifName); err != nil {
		return err
	}

	if err := clearMap(target.blocked); err != nil {
		return fmt.Errorf("clear blocked IP map for %q: %w", ifName, err)
	}

	return nil
}
