//go:build linux

package main

import (
	"fmt"

	"github.com/vishvananda/netlink"
)

func addVethPair(hostHandle *netlink.Handle, cfg vethPairConfig) (netlink.Link, netlink.Link, error) {
	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: cfg.HostIfName},
		PeerName:  cfg.TempPeerName,
	}

	if err := hostHandle.LinkAdd(veth); err != nil {
		if isAlreadyExists(err) {
			return nil, nil, conflictError{message: fmt.Sprintf("link %s or %s already exists", cfg.HostIfName, cfg.TempPeerName)}
		}
		return nil, nil, fmt.Errorf("create veth pair %s/%s: %w", cfg.HostIfName, cfg.TempPeerName, err)
	}

	hostLink, err := hostHandle.LinkByName(cfg.HostIfName)
	if err != nil {
		return nil, nil, fmt.Errorf("lookup host-side veth %s: %w", cfg.HostIfName, err)
	}

	peerLink, err := hostHandle.LinkByName(cfg.TempPeerName)
	if err != nil {
		return nil, nil, fmt.Errorf("lookup temporary peer veth %s: %w", cfg.TempPeerName, err)
	}

	return hostLink, peerLink, nil
}

func configureHostVeth(hostHandle *netlink.Handle, hostLink netlink.Link, cfg vethPairConfig) error {
	return addAddressAndSetUp(hostHandle, hostLink, cfg.HostIP, cfg.HostIfName, "host")
}

func configureNamespaceVeth(nsHandle *netlink.Handle, cfg vethPairConfig) error {
	peerLink, err := nsHandle.LinkByName(cfg.TempPeerName)
	if err != nil {
		return fmt.Errorf("lookup peer veth %s in %s: %w", cfg.TempPeerName, cfg.NamespacePath, err)
	}

	if err := nsHandle.LinkSetName(peerLink, cfg.PeerIfName); err != nil {
		return fmt.Errorf("rename peer veth %s to %s: %w", cfg.TempPeerName, cfg.PeerIfName, err)
	}

	peerLink, err = nsHandle.LinkByName(cfg.PeerIfName)
	if err != nil {
		return fmt.Errorf("lookup renamed peer veth %s: %w", cfg.PeerIfName, err)
	}

	if err := addAddressAndSetUp(nsHandle, peerLink, cfg.PeerIP, cfg.PeerIfName, "peer"); err != nil {
		return err
	}

	return setLoopbackUp(nsHandle, cfg.NamespacePath)
}

func addAddressAndSetUp(handle *netlink.Handle, link netlink.Link, cidr, ifName, side string) error {
	addr, err := netlink.ParseAddr(cidr)
	if err != nil {
		return fmt.Errorf("parse %s IP %q: %w", side, cidr, err)
	}

	if err := handle.AddrAdd(link, addr); err != nil && !isAlreadyExists(err) {
		return fmt.Errorf("add %s IP %s to %s: %w", side, cidr, ifName, err)
	}

	if err := handle.LinkSetUp(link); err != nil {
		return fmt.Errorf("set %s veth %s up: %w", side, ifName, err)
	}

	return nil
}

func setLoopbackUp(nsHandle *netlink.Handle, namespacePath string) error {
	loLink, err := nsHandle.LinkByName("lo")
	if err != nil {
		return fmt.Errorf("lookup lo in %s: %w", namespacePath, err)
	}

	if err := nsHandle.LinkSetUp(loLink); err != nil {
		return fmt.Errorf("set lo up in %s: %w", namespacePath, err)
	}

	return nil
}
