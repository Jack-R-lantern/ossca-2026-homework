//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func (m *bpfManager) handleAttach(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req attachRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid json body: %v", err))
		return
	}

	if req.IfName == "" {
		writeError(w, http.StatusBadRequest, "ifname is required")
		return
	}

	if err := m.Attach(req.IfName); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, attachResponse{
		IfName:   req.IfName,
		Hook:     "xdp",
		Attached: true,
	})
}

func (m *bpfManager) handleBlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ifName, ok := pathValue(r.URL.Path, "/bpf/block/")
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	var req blockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid json body: %v", err))
		return
	}

	if err := m.BlockIP(ifName, req.IP); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, blockResponse{
		IfName:    ifName,
		BlockedIP: req.IP,
	})
}

func (m *bpfManager) handleClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ifName, ok := pathValue(r.URL.Path, "/bpf/clear/")
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	if err := m.Clear(ifName); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, clearResponse{
		IfName:  ifName,
		Cleared: true,
	})
}

func pathValue(path, prefix string) (string, bool) {
	value := strings.TrimPrefix(path, prefix)
	if value == path || value == "" || strings.Contains(value, "/") {
		return "", false
	}
	return value, true
}
