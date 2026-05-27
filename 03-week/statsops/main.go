//go:build linux

package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"golang.org/x/sys/unix"
	"github.com/vishvananda/netlink"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang xdp xdp_kernel.c


type BlockedKey struct {
	Ifindex uint32
	IP      uint32
}

var (
	bpfObjects xdpObjects
)

func main() {

	if err := loadXdpObjects(&bpfObjects, nil); err != nil {
		log.Fatalf("failed to load BPF objects: %v", err)
	}

	defer bpfObjects.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/bpf/attach", handleAttach)
	mux.HandleFunc("/bpf/block/", handleBlock) // /bpf/block/{ifname}
	mux.HandleFunc("/bpf/clear/", handleClear) // /bpf/clear/{ifname}

	log.Println("eBPF Firewall API server listening on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}

}

type AttachRequest struct {
	IfName string `json:"ifname"`
}

type AttachResponse struct {
	IfName   string `json:"ifname"`
	Hook     string `json:"hook"`
	Attached bool   `json:"attached"`
}

func handleAttach(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AttachRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 1. veth 
	link, err := netlink.LinkByName(req.IfName)
	if err != nil {
		http.Error(w, fmt.Sprintf("interface not found: %s", req.IfName), http.StatusBadRequest)
		return
	}

	// 2. XDP Program Attach with netlink
	err = netlink.LinkSetXdpFdWithFlags(link, bpfObjects.XdpFirewall.FD(), unix.XDP_FLAGS_SKB_MODE)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to attach XDP: %v", err), http.StatusInternalServerError)
		return
	}

	// 3. JSON Response return
	resp := AttachResponse{
		IfName:   req.IfName,
		Hook:     "xdp",
		Attached: true,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
	
	}

	type BlockRequest struct {
	IP string `json:"ip"`
}

type BlockResponse struct {
	IfName    string `json:"ifname"`
	BlockedIP string `json:"blocked_ip"`
}

func handleBlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// ifname
	ifname := strings.TrimPrefix(r.URL.Path, "/bpf/block/")
	if ifname == "" {
		http.Error(w, "interface name is required", http.StatusBadRequest)
		return
	}

	// 1. interface and XDP attached check
	link, err := netlink.LinkByName(ifname)
	if err != nil {
		http.Error(w, fmt.Sprintf("interface not found: %s", ifname), http.StatusBadRequest)
		return
	}
	
	// XDP attached
	if link.Attrs().Xdp == nil || !link.Attrs().Xdp.Attached {
		http.Error(w, "XDP is not attached to this interface", http.StatusBadRequest)
		return
	}

	var req BlockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 2. IPv4 parsing and return IPv4
	ip := net.ParseIP(req.IP).To4()
	if ip == nil {
		http.Error(w, "invalid IPv4 address", http.StatusBadRequest)
		return
	}

	ipVal := binary.BigEndian.Uint32(ip)

	// 3. data in Map
	key := BlockedKey{
		Ifindex: uint32(link.Attrs().Index),
		IP:      ipVal,
	}
	value := uint32(1)

	// data insert in bpfObjects.BlockedIps Map
	if err := bpfObjects.BlockedIps.Put(&key, &value); err != nil {
		http.Error(w, fmt.Sprintf("failed to update BPF map: %v", err), http.StatusInternalServerError)
		return
	}

	// 4. Response
	resp := BlockResponse{
		IfName:    ifname,
		BlockedIP: req.IP,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
	}

	type ClearResponse struct {
	IfName  string `json:"ifname"`
	Cleared bool   `json:"cleared"`
}

func handleClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ifname := strings.TrimPrefix(r.URL.Path, "/bpf/clear/")
	link, err := netlink.LinkByName(ifname)
	if err != nil {
		http.Error(w, fmt.Sprintf("interface not found: %s", ifname), http.StatusBadRequest)
		return
	}

	targetIfindex := uint32(link.Attrs().Index)

	// 1. Key
	var keysToDelete []BlockedKey
	var key BlockedKey
	var val uint32

	// Map Iterator
	iter := bpfObjects.BlockedIps.Iterate()
	for iter.Next(&key, &val) {
		if key.Ifindex == targetIfindex {
			keysToDelete = append(keysToDelete, key)
		}
	}
	if err := iter.Err(); err != nil {
		http.Error(w, fmt.Sprintf("map iteration error: %v", err), http.StatusInternalServerError)
		return
	}

	// 2. Key Flush
	for _, k := range keysToDelete {
		if err := bpfObjects.BlockedIps.Delete(&k); err != nil {
			if !strings.Contains(err.Error(), "key does not exist") {
				http.Error(w, fmt.Sprintf("failed to delete map element: %v", err), http.StatusInternalServerError)
				return
			}
		}
	}

	// 3. Response with JSON return
	resp := ClearResponse{
		IfName:  ifname,
		Cleared: true,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
