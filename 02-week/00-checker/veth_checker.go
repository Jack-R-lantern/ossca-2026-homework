package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

type vethCheckRequest struct {
	NamespaceName string
	NamespacePath string
	HostIfName    string
	PeerIfName    string
	HostIP        string
	PeerIP        string
}

type linkSnapshot struct {
	Name  string
	Type  string
	Index int
	Flags net.Flags
}

type vethCheckState struct {
	HostLink      linkSnapshot
	PeerLink      linkSnapshot
	LoopbackLink  linkSnapshot
	HostAddrs     []netlink.Addr
	PeerAddrs     []netlink.Addr
	HostPeerIndex int
	PeerPeerIndex int
}

func runVethCheck(args []string) error {
	fs := flag.NewFlagSet("veth", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	request := vethCheckRequest{}
	fs.StringVar(&request.NamespaceName, "name", "", "named network namespace that should contain the peer interface")
	fs.StringVar(&request.NamespacePath, "path", "", "full path to the named network namespace mount")
	fs.StringVar(&request.HostIfName, "host-ifname", "", "host-side veth interface name")
	fs.StringVar(&request.PeerIfName, "peer-ifname", "", "namespace-side veth interface name")
	fs.StringVar(&request.HostIP, "host-ip", "", "host-side IPv4 address in CIDR notation")
	fs.StringVar(&request.PeerIP, "peer-ip", "", "namespace-side IPv4 address in CIDR notation")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: %s veth --name <namespace> --host-ifname <ifname> --peer-ifname <ifname> --host-ip <cidr> --peer-ip <cidr>\n", os.Args[0])
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() != 0 {
		fs.Usage()
		return errors.New("unexpected positional arguments")
	}

	if err := validateVethCheckRequest(&request); err != nil {
		return err
	}

	if err := checkVethConfiguration(request); err != nil {
		return err
	}

	fmt.Printf(
		"veth configuration verified: host=%s(%s) peer=%s(%s) namespace=%s\n",
		request.HostIfName,
		request.HostIP,
		request.PeerIfName,
		request.PeerIP,
		request.NamespacePath,
	)
	return nil
}

func validateVethCheckRequest(request *vethCheckRequest) error {
	if request.NamespacePath == "" {
		if request.NamespaceName == "" {
			return errors.New("either --name or --path must be provided")
		}

		request.NamespacePath = filepath.Join(namedNamespaceDir, request.NamespaceName)
	} else if request.NamespaceName == "" {
		request.NamespaceName = filepath.Base(request.NamespacePath)
	}

	if request.HostIfName == "" {
		return errors.New("host-ifname is required")
	}

	if request.PeerIfName == "" {
		return errors.New("peer-ifname is required")
	}

	if request.HostIP == "" {
		return errors.New("host-ip is required")
	}

	if request.PeerIP == "" {
		return errors.New("peer-ip is required")
	}

	if _, err := netlink.ParseAddr(request.HostIP); err != nil {
		return fmt.Errorf("host-ip must be a valid CIDR: %w", err)
	}

	if _, err := netlink.ParseAddr(request.PeerIP); err != nil {
		return fmt.Errorf("peer-ip must be a valid CIDR: %w", err)
	}

	return nil
}

func checkVethConfiguration(request vethCheckRequest) error {
	if os.Geteuid() != 0 {
		return errors.New("veth check requires root to open and enter named network namespaces")
	}

	if err := checkNamedNetNSMount(request.NamespacePath); err != nil {
		return err
	}

	hostHandle, err := netlink.NewHandle()
	if err != nil {
		return fmt.Errorf("open host netlink handle: %w", err)
	}
	defer hostHandle.Close()

	nsHandle, err := netns.GetFromPath(request.NamespacePath)
	if err != nil {
		return fmt.Errorf("open named network namespace %q: %w", request.NamespacePath, err)
	}
	defer nsHandle.Close()

	nsNetlink, err := netlink.NewHandleAt(nsHandle)
	if err != nil {
		return fmt.Errorf("open namespace netlink handle %q: %w", request.NamespacePath, err)
	}
	defer nsNetlink.Close()

	hostLink, err := hostHandle.LinkByName(request.HostIfName)
	if err != nil {
		return fmt.Errorf("lookup host-side link %q: %w", request.HostIfName, err)
	}

	peerLink, err := nsNetlink.LinkByName(request.PeerIfName)
	if err != nil {
		return fmt.Errorf("lookup namespace-side link %q in %s: %w", request.PeerIfName, request.NamespacePath, err)
	}

	loopbackLink, err := nsNetlink.LinkByName("lo")
	if err != nil {
		return fmt.Errorf("lookup loopback in %s: %w", request.NamespacePath, err)
	}

	hostAddrs, err := hostHandle.AddrList(hostLink, netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("list IPv4 addresses on host-side link %q: %w", request.HostIfName, err)
	}

	peerAddrs, err := nsNetlink.AddrList(peerLink, netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("list IPv4 addresses on namespace-side link %q: %w", request.PeerIfName, err)
	}

	hostPeerIndex, err := vethPeerIndexInCurrentNamespace(hostLink)
	if err != nil {
		return fmt.Errorf("read host-side peer index for %q: %w", request.HostIfName, err)
	}

	peerPeerIndex, err := vethPeerIndexInNamespace(request.NamespacePath, request.PeerIfName)
	if err != nil {
		return fmt.Errorf("read namespace-side peer index for %q: %w", request.PeerIfName, err)
	}

	return validateVethCheckState(request, vethCheckState{
		HostLink:      snapshotLink(hostLink),
		PeerLink:      snapshotLink(peerLink),
		LoopbackLink:  snapshotLink(loopbackLink),
		HostAddrs:     hostAddrs,
		PeerAddrs:     peerAddrs,
		HostPeerIndex: hostPeerIndex,
		PeerPeerIndex: peerPeerIndex,
	})
}

func validateVethCheckState(request vethCheckRequest, state vethCheckState) error {
	if state.HostLink.Type != "veth" {
		return fmt.Errorf("host-side link %q is type %q, want %q", request.HostIfName, state.HostLink.Type, "veth")
	}

	if state.PeerLink.Type != "veth" {
		return fmt.Errorf("namespace-side link %q is type %q, want %q", request.PeerIfName, state.PeerLink.Type, "veth")
	}

	if state.HostPeerIndex != state.PeerLink.Index {
		return fmt.Errorf(
			"host-side link %q is not paired with namespace-side link %q: host peer index=%d peer index=%d",
			request.HostIfName,
			request.PeerIfName,
			state.HostPeerIndex,
			state.PeerLink.Index,
		)
	}

	if state.PeerPeerIndex != state.HostLink.Index {
		return fmt.Errorf(
			"namespace-side link %q is not paired with host-side link %q: peer peer index=%d host index=%d",
			request.PeerIfName,
			request.HostIfName,
			state.PeerPeerIndex,
			state.HostLink.Index,
		)
	}

	if state.HostLink.Flags&net.FlagUp == 0 {
		return fmt.Errorf("host-side link %q is not UP", request.HostIfName)
	}

	if state.PeerLink.Flags&net.FlagUp == 0 {
		return fmt.Errorf("namespace-side link %q is not UP", request.PeerIfName)
	}

	if state.LoopbackLink.Flags&net.FlagUp == 0 {
		return errors.New("loopback link lo is not UP inside the named network namespace")
	}

	expectedHostAddr, err := netlink.ParseAddr(request.HostIP)
	if err != nil {
		return fmt.Errorf("parse expected host-ip %q: %w", request.HostIP, err)
	}

	if !containsAddr(state.HostAddrs, *expectedHostAddr) {
		return fmt.Errorf("host-side link %q does not have expected address %s", request.HostIfName, request.HostIP)
	}

	expectedPeerAddr, err := netlink.ParseAddr(request.PeerIP)
	if err != nil {
		return fmt.Errorf("parse expected peer-ip %q: %w", request.PeerIP, err)
	}

	if !containsAddr(state.PeerAddrs, *expectedPeerAddr) {
		return fmt.Errorf("namespace-side link %q does not have expected address %s", request.PeerIfName, request.PeerIP)
	}

	return nil
}

func snapshotLink(link netlink.Link) linkSnapshot {
	attrs := link.Attrs()
	return linkSnapshot{
		Name:  attrs.Name,
		Type:  link.Type(),
		Index: attrs.Index,
		Flags: attrs.Flags,
	}
}

func containsAddr(addrs []netlink.Addr, expected netlink.Addr) bool {
	for _, addr := range addrs {
		if addr.Equal(expected) {
			return true
		}
	}

	return false
}

func vethPeerIndexInCurrentNamespace(link netlink.Link) (int, error) {
	if link.Type() != "veth" {
		return 0, fmt.Errorf("link %q is type %q, want %q", link.Attrs().Name, link.Type(), "veth")
	}

	return netlink.VethPeerIndex(&netlink.Veth{LinkAttrs: *link.Attrs()})
}

func vethPeerIndexInNamespace(namespacePath, linkName string) (int, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	originNS, err := netns.Get()
	if err != nil {
		return 0, fmt.Errorf("get current network namespace: %w", err)
	}
	defer originNS.Close()

	targetNS, err := netns.GetFromPath(namespacePath)
	if err != nil {
		return 0, fmt.Errorf("open named network namespace %q: %w", namespacePath, err)
	}
	defer targetNS.Close()

	if err := netns.Set(targetNS); err != nil {
		return 0, fmt.Errorf("set current thread to network namespace %q: %w", namespacePath, err)
	}

	restoreOrigin := true
	defer func() {
		if restoreOrigin {
			_ = netns.Set(originNS)
		}
	}()

	link, err := netlink.LinkByName(linkName)
	if err != nil {
		return 0, fmt.Errorf("lookup link %q after entering %q: %w", linkName, namespacePath, err)
	}

	peerIndex, err := vethPeerIndexInCurrentNamespace(link)
	if err != nil {
		return 0, err
	}

	if err := netns.Set(originNS); err != nil {
		return 0, fmt.Errorf("restore original network namespace after reading peer index for %q: %w", linkName, err)
	}

	restoreOrigin = false
	return peerIndex, nil
}
