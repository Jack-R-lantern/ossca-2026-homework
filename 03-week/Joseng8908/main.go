//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/cilium/ebpf/link"
)

type attachReq struct {
	IfName string `json:"ifname"`
}

type attachResp struct {
	IfName   string `json:"ifname"`
	Hook     string `json:"hook"`
	Attached bool   `json:"attached"`
}

type ipReq struct {
	IP string `json:"ip"`
}

type blockResp struct {
	IfName    string `json:"ifname"`
	BlockedIP string `json:"blocked_ip"`
}

type clearResp struct {
	IfName  string `json:"ifname"`
	Cleared bool   `json:"cleared"`
}

type errResp struct {
	Error string `json:"error"`
}

type ifEntry struct {
	objs    xdpFilterObjects
	xdpLink link.Link
}

var (
	mu     sync.RWMutex
	ifaces = map[string]*ifEntry{}
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func handleAttach(w http.ResponseWriter, r *http.Request) {
	var req attachReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp{err.Error()})
		return
	}
	if req.IfName == "" {
		writeJSON(w, http.StatusBadRequest, errResp{"ifname is required"})
		return
	}

	iface, err := net.InterfaceByName(req.IfName)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errResp{fmt.Sprintf("interface %q not found: %v", req.IfName, err)})
		return
	}

	mu.Lock()
	defer mu.Unlock()

	if _, exists := ifaces[req.IfName]; exists {
		writeJSON(w, http.StatusOK, attachResp{IfName: req.IfName, Hook: "xdp", Attached: true})
		return
	}

	var objs xdpFilterObjects
	if err := loadXdpFilterObjects(&objs, nil); err != nil {
		writeJSON(w, http.StatusInternalServerError, errResp{fmt.Sprintf("load bpf objects: %v", err)})
		return
	}

	xdpLink, err := link.AttachXDP(link.XDPOptions{
		Program:   objs.XdpFilter,
		Interface: iface.Index,
		Flags:     link.XDPGenericMode,
	})
	if err != nil {
		objs.Close()
		writeJSON(w, http.StatusInternalServerError, errResp{fmt.Sprintf("attach xdp: %v", err)})
		return
	}

	ifaces[req.IfName] = &ifEntry{objs: objs, xdpLink: xdpLink}
	writeJSON(w, http.StatusOK, attachResp{IfName: req.IfName, Hook: "xdp", Attached: true})
}

func handleBlock(w http.ResponseWriter, r *http.Request) {
	ifname := r.PathValue("ifname")

	var req ipReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp{err.Error()})
		return
	}

	ip4 := net.ParseIP(req.IP).To4()
	if ip4 == nil {
		writeJSON(w, http.StatusBadRequest, errResp{"invalid IPv4 address"})
		return
	}

	mu.RLock()
	entry, ok := ifaces[ifname]
	mu.RUnlock()

	if !ok {
		writeJSON(w, http.StatusNotFound, errResp{fmt.Sprintf("interface %q not attached", ifname)})
		return
	}

	// ip->saddr in the XDP program is the raw 4 bytes from the packet (network byte order).
	// Interpreting those same bytes as a little-endian uint32 gives the key value that
	// cilium/ebpf will encode back into the same bytes when it writes to the map.
	key := binary.LittleEndian.Uint32(ip4)
	val := uint8(1)
	if err := entry.objs.BlockedIps.Put(&key, &val); err != nil {
		writeJSON(w, http.StatusInternalServerError, errResp{fmt.Sprintf("update bpf map: %v", err)})
		return
	}

	writeJSON(w, http.StatusOK, blockResp{IfName: ifname, BlockedIP: req.IP})
}

func handleClear(w http.ResponseWriter, r *http.Request) {
	ifname := r.PathValue("ifname")

	mu.RLock()
	entry, ok := ifaces[ifname]
	mu.RUnlock()

	if !ok {
		writeJSON(w, http.StatusNotFound, errResp{fmt.Sprintf("interface %q not attached", ifname)})
		return
	}

	var toDelete []uint32
	var key uint32
	var val uint8
	iter := entry.objs.BlockedIps.Iterate()
	for iter.Next(&key, &val) {
		toDelete = append(toDelete, key)
	}
	if err := iter.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, errResp{fmt.Sprintf("iterate bpf map: %v", err)})
		return
	}
	for i := range toDelete {
		_ = entry.objs.BlockedIps.Delete(&toDelete[i])
	}

	writeJSON(w, http.StatusOK, clearResp{IfName: ifname, Cleared: true})
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /bpf/attach", handleAttach)
	mux.HandleFunc("POST /bpf/block/{ifname}", handleBlock)
	mux.HandleFunc("POST /bpf/clear/{ifname}", handleClear)

	server := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Println("listening on :8080")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
