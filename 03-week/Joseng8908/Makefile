BINARY     := app
BPF2GO     := go run github.com/cilium/ebpf/cmd/bpf2go
LLVM_STRIP := $(shell command -v llvm-strip 2>/dev/null || ls /usr/bin/llvm-strip-* 2>/dev/null | sort -rV | head -1)
STRIP_FLAG := $(if $(LLVM_STRIP),-strip $(LLVM_STRIP))
# Ubuntu stores arch-specific headers under /usr/include/<multiarch>; RHEL doesn't need this.
MULTIARCH  := $(shell dpkg-architecture -qDEB_HOST_MULTIARCH 2>/dev/null)
CFLAGS     := $(if $(MULTIARCH),-I/usr/include/$(MULTIARCH))

.PHONY: generate build run clean

generate:
	$(BPF2GO) -cc clang $(STRIP_FLAG) -go-package main xdpFilter ./bpf/xdp_filter.c -- $(CFLAGS)

build: generate
	go build -o $(BINARY) .

run: build
	sudo ./$(BINARY)

clean:
	rm -f $(BINARY) xdpFilter_bpfel.go xdpFilter_bpfeb.go xdpFilter_bpfel.o xdpFilter_bpfeb.o
