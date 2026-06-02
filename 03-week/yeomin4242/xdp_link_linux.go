//go:build linux

package main

import (
	"fmt"

	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
)

func loadAndAttachXDP(ifIndex int) (*attachedInterface, error) {
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("raise memlock rlimit: %w", err)
	}

	coll, err := loadXDPCollection()
	if err != nil {
		return nil, err
	}

	prog := coll.Programs[xdpProgramName]
	if prog == nil {
		coll.Close()
		return nil, fmt.Errorf("eBPF program %q not found", xdpProgramName)
	}

	blocked := coll.Maps[blockedMapName]
	if blocked == nil {
		coll.Close()
		return nil, fmt.Errorf("eBPF map %q not found", blockedMapName)
	}

	xdpLink, err := link.AttachXDP(link.XDPOptions{
		Program:   prog,
		Interface: ifIndex,
		Flags:     link.XDPGenericMode,
	})
	if err != nil {
		coll.Close()
		return nil, err
	}

	info, err := prog.Info()
	if err != nil {
		xdpLink.Close()
		coll.Close()
		return nil, fmt.Errorf("read eBPF program info: %w", err)
	}

	progID, ok := info.ID()
	if !ok {
		xdpLink.Close()
		coll.Close()
		return nil, fmt.Errorf("eBPF program ID is unavailable")
	}

	return &attachedInterface{
		ifIndex: ifIndex,
		progID:  uint32(progID),
		link:    xdpLink,
		coll:    coll,
		blocked: blocked,
	}, nil
}
