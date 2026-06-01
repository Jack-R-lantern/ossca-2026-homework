BINARY     := app
BPF2GO     := go run github.com/cilium/ebpf/cmd/bpf2go
LLVM_STRIP := $(shell command -v llvm-strip 2>/dev/null || ls /usr/bin/llvm-strip-* 2>/dev/null | sort -rV | head -1)
STRIP_FLAG := $(if $(LLVM_STRIP),-strip $(LLVM_STRIP))

.PHONY: generate build run clean

generate:
	$(BPF2GO) -cc clang $(STRIP_FLAG) xdpFilter ./bpf/xdp_filter.c

build: generate
	go build -o $(BINARY) .

run: build
	sudo ./$(BINARY)

clean:
	rm -f $(BINARY) xdpFilter_bpfel.go xdpFilter_bpfeb.go xdpFilter_bpfel.o xdpFilter_bpfeb.o
