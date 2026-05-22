//go:build linux

package main

import (
	"bytes"
	_ "embed"
	"fmt"

	"github.com/cilium/ebpf"
)

//go:embed bpf/xdp_filter_bpfel.o
var xdpFilterObject []byte

const (
	xdpProgramName = "xdp_block_ipv4"
	blockedMapName = "blocked_ips"
)

func loadXDPCollection() (*ebpf.Collection, error) {
	spec, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(xdpFilterObject))
	if err != nil {
		return nil, fmt.Errorf("load eBPF collection spec: %w", err)
	}

	coll, err := ebpf.NewCollection(spec)
	if err != nil {
		return nil, fmt.Errorf("load eBPF collection: %w", err)
	}

	return coll, nil
}
