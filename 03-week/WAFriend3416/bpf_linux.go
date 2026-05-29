//go:build linux

package main

// Linux 빌드에서만 eBPF C 코드를 Go 바인딩으로 생성한다.
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -no-strip -target bpfel bpf bpf/xdp_block.c -- -Wall -Werror
