//go:build linux

package main

import (
	"fmt"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/vishvananda/netlink"
)

type attachedInterface struct {
	ifIndex int
	progID  uint32
	link    link.Link
	coll    *ebpf.Collection
	blocked *ebpf.Map
}

func (a *attachedInterface) Close() {
	if a.link != nil {
		_ = a.link.Close()
	}
	if a.coll != nil {
		a.coll.Close()
	}
}

func (a *attachedInterface) verifyAttached(ifName string) error {
	iface, err := netlink.LinkByName(ifName)
	if err != nil {
		return fmt.Errorf("lookup interface %q: %w", ifName, err)
	}

	attrs := iface.Attrs()
	if attrs.Index != a.ifIndex {
		return fmt.Errorf("interface %q index changed from %d to %d", ifName, a.ifIndex, attrs.Index)
	}

	xdp := attrs.Xdp
	if xdp == nil || !xdp.Attached || xdp.ProgId == 0 {
		return fmt.Errorf("XDP program is not attached to %q", ifName)
	}

	if xdp.ProgId != a.progID {
		return fmt.Errorf("XDP program on %q changed from %d to %d", ifName, a.progID, xdp.ProgId)
	}

	return nil
}
