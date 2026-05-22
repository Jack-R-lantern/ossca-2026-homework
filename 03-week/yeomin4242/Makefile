.PHONY: build clean

APP := app
BPF_SRC := bpf/xdp_filter.c
BPF_OBJ := bpf/xdp_filter_bpfel.o

CLANG ?= $(firstword $(wildcard /opt/homebrew/opt/llvm/bin/clang) $(shell command -v clang 2>/dev/null))
LLVM_STRIP ?= $(firstword $(wildcard /opt/homebrew/opt/llvm/bin/llvm-strip) $(shell command -v llvm-strip 2>/dev/null))

build: $(BPF_OBJ)
	go build -o $(APP) .

$(BPF_OBJ): $(BPF_SRC)
	@test -n "$(CLANG)" || (echo "clang is required to build the eBPF object" >&2; exit 1)
	$(CLANG) -O2 -g -target bpfel -c $(BPF_SRC) -o $(BPF_OBJ)
	@if [ -n "$(LLVM_STRIP)" ]; then $(LLVM_STRIP) -g $(BPF_OBJ); fi

clean:
	rm -f $(APP)
