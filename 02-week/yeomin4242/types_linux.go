//go:build linux

package main

const (
	listenAddr = ":8080"
	netnsDir   = "/var/run/netns"
)

type createNetnsRequest struct {
	Name string `json:"name"`
}

type createNetnsResponse struct {
	Name      string `json:"name"`
	NetnsPath string `json:"netns_path"`
}

type createVethRequest struct {
	HostIfname string `json:"host_ifname"`
	PeerIfname string `json:"peer_ifname"`
	HostIP     string `json:"host_ip"`
	PeerIP     string `json:"peer_ip"`
}

type createVethResponse struct {
	Name       string `json:"name"`
	HostIfname string `json:"host_ifname"`
	PeerIfname string `json:"peer_ifname"`
	HostIP     string `json:"host_ip"`
	PeerIP     string `json:"peer_ip"`
	NetnsPath  string `json:"netns_path"`
}

type execRequest struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

type execResponse struct {
	Name      string `json:"name"`
	ParentPID int    `json:"parent_pid"`
	ChildPID  int    `json:"child_pid"`
}

type errorResponse struct {
	Error string `json:"error"`
}
