//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

const netnsDir = "/var/run/netns"

type createNetnsReq struct {
	Name string `json:"name"`
}

type createNetnsResp struct {
	Name      string `json:"name"`
	NetnsPath string `json:"netns_path"`
}

type createVethReq struct {
	HostIfname string `json:"host_ifname"`
	PeerIfname string `json:"peer_ifname"`
	HostIP     string `json:"host_ip"`
	PeerIP     string `json:"peer_ip"`
}

type execReq struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

type execResp struct {
	Name      string `json:"name"`
	ParentPID int    `json:"parent_pid"`
	ChildPID  int    `json:"child_pid"`
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /netns", handleCreateNetns)
	mux.HandleFunc("POST /netns/{name}/veth", handleCreateVeth)
	mux.HandleFunc("POST /netns/{name}/exec", handleExec)

	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

func handleCreateNetns(w http.ResponseWriter, r *http.Request) {
	var req createNetnsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
	writeJSON(w, http.StatusOK, createNetnsResp{Name: req.Name, NetnsPath: path})
}

func handleCreateVeth(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req createVethReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := createVeth(name, req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

func handleExec(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req execReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	parentPID, childPID, err := execInNetns(name, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, execResp{
		Name:      name,
		ParentPID: parentPID,
		ChildPID:  childPID,
	})
}

func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if strings.ContainsAny(name, "/\x00") || name == ".." || name == "." {
		return fmt.Errorf("invalid namespace name: %q", name)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	log.Printf("error: %s", msg)
	writeJSON(w, status, map[string]string{"error": msg})
}
