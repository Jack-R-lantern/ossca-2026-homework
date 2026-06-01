//go:build linux

package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func deleteVethIfExists(handle *netlink.Handle, name string) error {
	link, err := handle.LinkByName(name)
	if err != nil {
		if isNotFound(err) {
			return nil
		}

		return fmt.Errorf("lookup link %s: %w", name, err)
	}

	if link.Type() != "veth" {
		return conflictError{message: fmt.Sprintf("link %s already exists as type %q; refusing to delete non-veth link", name, link.Type())}
	}

	if err := handle.LinkDel(link); err != nil {
		return fmt.Errorf("delete existing link %s: %w", name, err)
	}

	return nil
}

func ensureLinkAbsent(handle *netlink.Handle, name string) error {
	link, err := handle.LinkByName(name)
	if err != nil {
		if isNotFound(err) {
			return nil
		}

		return fmt.Errorf("lookup link %s: %w", name, err)
	}

	return conflictError{message: fmt.Sprintf("link %s already exists as type %q", name, link.Type())}
}

type conflictError struct {
	message string
}

func (e conflictError) Error() string {
	return e.message
}

func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}

	return errors.Is(err, unix.EEXIST) || strings.Contains(strings.ToLower(err.Error()), "file exists")
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}

	lower := strings.ToLower(err.Error())
	return errors.Is(err, unix.ENODEV) ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "no such network interface") ||
		strings.Contains(lower, "link not found")
}
