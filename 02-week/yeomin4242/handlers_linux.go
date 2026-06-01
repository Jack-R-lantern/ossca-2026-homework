//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
)

func handleCreateNetns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req createNetnsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid json body: %v", err))
		return
	}

	if err := validateName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	path, err := createNamedNetns(req.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, createNetnsResponse{
		Name:      req.Name,
		NetnsPath: path,
	})
}

func handleNetnsAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	name, action, err := parseNetnsAction(r.URL.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	if err := validateName(name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	switch action {
	case "veth":
		handleCreateVeth(w, r, name)
	case "exec":
		handleExec(w, r, name)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func handleCreateVeth(w http.ResponseWriter, r *http.Request, name string) {
	var req createVethRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid json body: %v", err))
		return
	}

	if err := validateVethRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := createVethPair(name, req); err != nil {
		status := http.StatusInternalServerError
		var conflict conflictError
		if errors.As(err, &conflict) {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, createVethResponse{
		Name:       name,
		HostIfname: req.HostIfname,
		PeerIfname: req.PeerIfname,
		HostIP:     req.HostIP,
		PeerIP:     req.PeerIP,
		NetnsPath:  netnsPath(name),
	})
}

func handleExec(w http.ResponseWriter, r *http.Request, name string) {
	var req execRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid json body: %v", err))
		return
	}

	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	if !filepath.IsAbs(req.Path) {
		writeError(w, http.StatusBadRequest, "path must be absolute")
		return
	}

	parentPID, childPID, err := execInNetns(name, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, execResponse{
		Name:      name,
		ParentPID: parentPID,
		ChildPID:  childPID,
	})
}
