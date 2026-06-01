BINARY := app

# Required packages on RHEL/Fedora:
#   dnf install -y clang llvm libbpf-devel kernel-headers

.PHONY: generate build run clean

generate:
	go generate ./...

build: generate
	go build -o $(BINARY) .

run: build
	sudo ./$(BINARY)

clean:
	rm -f $(BINARY) xdpFilter_bpfel.go xdpFilter_bpfeb.go xdpFilter_bpfel.o xdpFilter_bpfeb.o
