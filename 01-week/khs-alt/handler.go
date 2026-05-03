package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"syscall"
)

func UserHandler(w http.ResponseWriter, r *http.Request) {
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

	output, err := MakeProcess(input.Path, input.Args)
	if err != nil {
		http.Error(w, fmt.Sprintf("makeProcess error: %v", err), http.StatusInternalServerError)
		return
	}

	// wait child process
	go func() {
		var wstatus syscall.WaitStatus
		_, err := syscall.Wait4(output.ChildPid, &wstatus, 0, nil)
		if err != nil {
			fmt.Printf("wait4 error: %v\n", err)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(output)
}
