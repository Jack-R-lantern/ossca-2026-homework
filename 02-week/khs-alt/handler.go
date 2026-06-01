package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func UserNSHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input Input
	err := json.NewDecoder(r.Body).Decode(&input)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := input.Validate(); err != nil {
		http.Error(w, fmt.Sprintf("input validate error: %v", err), http.StatusBadRequest)
		return
	}

	output, err := MakeProcess(input.Name)
	if err != nil {
		http.Error(w, fmt.Sprintf("makeProcess error: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(output)
}

func VethHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.PathValue("name")

	var input VethInput
	err := json.NewDecoder(r.Body).Decode(&input)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := input.Validate(); err != nil {
		http.Error(w, fmt.Sprintf("input validate error: %v", err), http.StatusBadRequest)
		return
	}

	if err := AddVeth(name, input.HostIfname, input.PeerIfname, input.HostIP, input.PeerIP); err != nil {
		http.Error(w, fmt.Sprintf("addVeth error: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func ExecHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.PathValue("name")

	var input ExecInput
	err := json.NewDecoder(r.Body).Decode(&input)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := input.Validate(); err != nil {
		http.Error(w, fmt.Sprintf("input validate error: %v", err), http.StatusBadRequest)
		return
	}

	parentPid, childPid, err := ExecInNetns(name, input.Path, input.Args)
	if err != nil {
		http.Error(w, fmt.Sprintf("execInNetns error: %v", err), http.StatusInternalServerError)
		return
	}

	output := ExecOutput{
		Name:      name,
		ParentPid: parentPid,
		ChildPid:  childPid,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(output)
}
