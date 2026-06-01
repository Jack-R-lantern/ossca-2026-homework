package main

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang xdpFilter ./bpf/xdp_filter.c
