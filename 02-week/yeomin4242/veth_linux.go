//go:build linux

package main

import (
	"fmt"

	"github.com/vishvananda/netns"
)

func createVethPair(name string, req createVethRequest) (err error) {
	cfg := newVethPairConfig(name, req)

	targetNS, err := netns.GetFromPath(cfg.NamespacePath)
	if err != nil {
		return fmt.Errorf("open named network namespace %s: %w", cfg.NamespacePath, err)
	}
	defer targetNS.Close()

	handles, err := openNetlinkHandles(targetNS)
	if err != nil {
		return err
	}
	defer handles.close()

	if err := ensureVethNamesAvailable(handles, cfg); err != nil {
		return err
	}

	hostLink, peerLink, err := addVethPair(handles.host, cfg)
	if err != nil {
		return err
	}

	vethCreated := true
	defer func() {
		if err != nil && vethCreated {
			_ = deleteVethIfExists(handles.host, cfg.HostIfName)
		}
	}()

	if err := handles.host.LinkSetNsFd(peerLink, int(targetNS)); err != nil {
		return fmt.Errorf("move peer veth %s to %s: %w", cfg.TempPeerName, cfg.NamespacePath, err)
	}

	if err := configureHostVeth(handles.host, hostLink, cfg); err != nil {
		return err
	}

	if err := configureNamespaceVeth(handles.ns, cfg); err != nil {
		return err
	}

	vethCreated = false
	return nil
}
