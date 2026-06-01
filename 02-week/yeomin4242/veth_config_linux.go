//go:build linux

package main

import (
	"fmt"
	"hash/fnv"
)

type vethPairConfig struct {
	NamespacePath string
	HostIfName    string
	PeerIfName    string
	TempPeerName  string
	HostIP        string
	PeerIP        string
}

func newVethPairConfig(name string, req createVethRequest) vethPairConfig {
	return vethPairConfig{
		NamespacePath: netnsPath(name),
		HostIfName:    req.HostIfname,
		PeerIfName:    req.PeerIfname,
		TempPeerName:  tempPeerName(req.HostIfname, req.PeerIfname),
		HostIP:        req.HostIP,
		PeerIP:        req.PeerIP,
	}
}

func tempPeerName(hostIfname, peerIfname string) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(hostIfname))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(peerIfname))

	return fmt.Sprintf("tmp%x", hash.Sum32())
}
