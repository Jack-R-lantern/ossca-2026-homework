//go:build linux

package main

import (
	"fmt"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

type netlinkHandles struct {
	host *netlink.Handle
	ns   *netlink.Handle
}

func openNetlinkHandles(targetNS netns.NsHandle) (netlinkHandles, error) {
	hostHandle, err := netlink.NewHandle()
	if err != nil {
		return netlinkHandles{}, fmt.Errorf("open host netlink handle: %w", err)
	}

	nsHandle, err := netlink.NewHandleAt(targetNS)
	if err != nil {
		hostHandle.Close()
		return netlinkHandles{}, fmt.Errorf("open namespace netlink handle: %w", err)
	}

	return netlinkHandles{
		host: hostHandle,
		ns:   nsHandle,
	}, nil
}

func (handles netlinkHandles) close() {
	if handles.host != nil {
		handles.host.Close()
	}

	if handles.ns != nil {
		handles.ns.Close()
	}
}
