//go:build linux

package main

import (
	"encoding/json"
	"net/http"
)

const maxJSONBodyBytes = 1 << 20

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	return json.NewDecoder(r.Body).Decode(dst)
}
