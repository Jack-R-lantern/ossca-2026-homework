//go:build linux

package main

const listenAddr = ":8080"

type attachRequest struct {
	IfName string `json:"ifname"`
}

type attachResponse struct {
	IfName   string `json:"ifname"`
	Hook     string `json:"hook"`
	Attached bool   `json:"attached"`
}

type blockRequest struct {
	IP string `json:"ip"`
}

type blockResponse struct {
	IfName    string `json:"ifname"`
	BlockedIP string `json:"blocked_ip"`
}

type clearResponse struct {
	IfName  string `json:"ifname"`
	Cleared bool   `json:"cleared"`
}

type errorResponse struct {
	Error string `json:"error"`
}
