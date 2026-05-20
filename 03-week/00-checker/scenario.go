//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

type attachRequest struct {
	IfName string `json:"ifname"`
}

type attachResponse struct {
	IfName   string `json:"ifname"`
	Hook     string `json:"hook"`
	Attached bool   `json:"attached"`
}

type ipRequest struct {
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

func runScenario(cfg scenarioConfig) error {
	if os.Geteuid() != 0 {
		return errors.New("checker requires root to create namespaces and veth interfaces")
	}

	cleanup := newCleanup()
	defer cleanup.Run()

	if err := cleanupNetwork(cfg); err != nil {
		return err
	}

	if err := setupNetwork(cfg, cleanup); err != nil {
		return err
	}

	attachResp := attachResponse{}
	if err := postJSON(cfg.ServerURL+"/bpf/attach", attachRequest{IfName: cfg.HostIfName}, &attachResp); err != nil {
		return err
	}

	if !attachResp.Attached || attachResp.Hook != "xdp" || attachResp.IfName != cfg.HostIfName {
		return fmt.Errorf("unexpected attach response: %+v", attachResp)
	}

	if err := verifyXDPAttached(cfg.HostIfName); err != nil {
		return err
	}

	if err := pingFromPeer(cfg); err != nil {
		return fmt.Errorf("pre-block ping failed: %w", err)
	}

	blockResp := blockResponse{}
	if err := postJSON(
		cfg.ServerURL+"/bpf/block/"+cfg.HostIfName,
		ipRequest{IP: cfg.PeerAddr.String()},
		&blockResp,
	); err != nil {
		return err
	}

	if blockResp.IfName != cfg.HostIfName || blockResp.BlockedIP != cfg.PeerAddr.String() {
		return fmt.Errorf("unexpected block response: %+v", blockResp)
	}

	if err := pingFromPeer(cfg); err == nil {
		return errors.New("packet drop verification failed: ping succeeded after block")
	}

	clearResp := clearResponse{}
	if err := postJSON(
		cfg.ServerURL+"/bpf/clear/"+cfg.HostIfName,
		ipRequest{IP: cfg.PeerAddr.String()},
		&clearResp,
	); err != nil {
		return err
	}

	if clearResp.IfName != cfg.HostIfName || !clearResp.Cleared {
		return fmt.Errorf("unexpected clear response: %+v", clearResp)
	}

	if err := pingFromPeer(cfg); err != nil {
		return fmt.Errorf("post-clear ping failed: %w", err)
	}

	return nil
}

type cleanupStack struct {
	funcs []func()
}

func newCleanup() *cleanupStack {
	return &cleanupStack{}
}

func (c *cleanupStack) Add(fn func()) {
	c.funcs = append(c.funcs, fn)
}

func (c *cleanupStack) Run() {
	for i := len(c.funcs) - 1; i >= 0; i-- {
		c.funcs[i]()
	}
}

func cleanupNetwork(cfg scenarioConfig) error {
	if link, err := netlink.LinkByName(cfg.HostIfName); err == nil {
		_ = netlink.LinkDel(link)
	}

	_ = netns.DeleteNamed(cfg.NamespaceName)
	return nil
}

func setupNetwork(cfg scenarioConfig, cleanup *cleanupStack) error {
	tmpPeerName := tempPeerName(cfg.HostIfName)

	targetNS, err := createNamedNetns(cfg.NamespaceName)
	if err != nil {
		return fmt.Errorf("create netns %q: %w", cfg.NamespaceName, err)
	}
	defer targetNS.Close()

	cleanup.Add(func() {
		_ = netns.DeleteNamed(cfg.NamespaceName)
	})

	hostHandle, err := netlink.NewHandle()
	if err != nil {
		return fmt.Errorf("open host netlink handle: %w", err)
	}
	defer hostHandle.Close()

	nsHandle, err := netlink.NewHandleAt(targetNS)
	if err != nil {
		return fmt.Errorf("open netlink handle in %q: %w", cfg.NamespaceName, err)
	}
	defer nsHandle.Close()

	attrs := netlink.NewLinkAttrs()
	attrs.Name = cfg.HostIfName
	veth := &netlink.Veth{
		LinkAttrs: attrs,
		PeerName:  tmpPeerName,
	}

	if err := hostHandle.LinkAdd(veth); err != nil {
		return fmt.Errorf("create veth pair %q/%q: %w", cfg.HostIfName, tmpPeerName, err)
	}

	cleanup.Add(func() {
		if link, err := netlink.LinkByName(cfg.HostIfName); err == nil {
			_ = netlink.LinkDel(link)
		}
	})

	hostLink, err := hostHandle.LinkByName(cfg.HostIfName)
	if err != nil {
		return fmt.Errorf("lookup host link %q: %w", cfg.HostIfName, err)
	}

	peerLink, err := hostHandle.LinkByName(tmpPeerName)
	if err != nil {
		return fmt.Errorf("lookup temporary peer link %q: %w", tmpPeerName, err)
	}

	if err := hostHandle.LinkSetNsFd(peerLink, int(targetNS)); err != nil {
		return fmt.Errorf("move peer veth %q into netns %q: %w", tmpPeerName, cfg.NamespaceName, err)
	}

	hostAddr, err := netlink.ParseAddr(cfg.HostCIDR)
	if err != nil {
		return fmt.Errorf("parse host ip %q: %w", cfg.HostCIDR, err)
	}

	if err := hostHandle.AddrAdd(hostLink, hostAddr); err != nil {
		return fmt.Errorf("configure host ip %q on %q: %w", cfg.HostCIDR, cfg.HostIfName, err)
	}

	if err := hostHandle.LinkSetUp(hostLink); err != nil {
		return fmt.Errorf("set host interface %q up: %w", cfg.HostIfName, err)
	}

	peerLink, err = nsHandle.LinkByName(tmpPeerName)
	if err != nil {
		return fmt.Errorf("lookup peer link %q in %q: %w", tmpPeerName, cfg.NamespaceName, err)
	}

	if err := nsHandle.LinkSetName(peerLink, cfg.PeerIfName); err != nil {
		return fmt.Errorf("rename peer veth %q to %q: %w", tmpPeerName, cfg.PeerIfName, err)
	}

	peerLink, err = nsHandle.LinkByName(cfg.PeerIfName)
	if err != nil {
		return fmt.Errorf("lookup renamed peer link %q in %q: %w", cfg.PeerIfName, cfg.NamespaceName, err)
	}

	peerAddr, err := netlink.ParseAddr(cfg.PeerCIDR)
	if err != nil {
		return fmt.Errorf("parse peer ip %q: %w", cfg.PeerCIDR, err)
	}

	if err := nsHandle.AddrAdd(peerLink, peerAddr); err != nil {
		return fmt.Errorf("configure peer ip %q on %q: %w", cfg.PeerCIDR, cfg.PeerIfName, err)
	}

	if err := nsHandle.LinkSetUp(peerLink); err != nil {
		return fmt.Errorf("set peer interface %q up: %w", cfg.PeerIfName, err)
	}

	loLink, err := nsHandle.LinkByName("lo")
	if err != nil {
		return fmt.Errorf("lookup loopback in %q: %w", cfg.NamespaceName, err)
	}

	if err := nsHandle.LinkSetUp(loLink); err != nil {
		return fmt.Errorf("set loopback up in %q: %w", cfg.NamespaceName, err)
	}

	return nil
}

func pingFromPeer(cfg scenarioConfig) error {
	return withNamedNetns(cfg.NamespaceName, func() error {
		_, err := runCommand(
			"ping",
			"-n",
			"-c", fmt.Sprintf("%d", cfg.PingCount),
			"-W", cfg.PingTimeoutArg(),
			"-I", cfg.PeerAddr.String(),
			cfg.HostAddr.String(),
		)
		return err
	})
}

func verifyXDPAttached(ifName string) error {
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return fmt.Errorf("inspect interface %q for xdp attachment: %w", ifName, err)
	}

	xdp := link.Attrs().Xdp
	if xdp == nil || !xdp.Attached || xdp.ProgId == 0 {
		return fmt.Errorf("interface %q does not have an attached xdp program", ifName)
	}

	return nil
}

func postJSON(url string, reqBody any, out any) error {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request for %s: %w", url, err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request for %s: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post %s: %w", url, err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body from %s: %w", url, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := errorResponse{}
		if err := json.Unmarshal(responseBody, &apiErr); err == nil && apiErr.Error != "" {
			return fmt.Errorf("%s returned %d: %s", url, resp.StatusCode, apiErr.Error)
		}
		return fmt.Errorf("%s returned %d: %s", url, resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(responseBody, out); err != nil {
		return fmt.Errorf("decode response from %s: %w", url, err)
	}

	return nil
}

func runCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return string(output), nil
	}

	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}

	return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, trimmed)
}

func tempPeerName(hostIfName string) string {
	suffix := hostIfName
	if len(suffix) > 8 {
		suffix = suffix[len(suffix)-8:]
	}

	name := "p" + suffix
	if len(name) > 15 {
		name = name[:15]
	}

	return name
}

func createNamedNetns(name string) (netns.NsHandle, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	originNS, err := netns.Get()
	if err != nil {
		return netns.None(), fmt.Errorf("get current netns: %w", err)
	}
	defer originNS.Close()

	targetNS, err := netns.NewNamed(name)
	if err != nil {
		return netns.None(), fmt.Errorf("create named netns: %w", err)
	}

	if err := netns.Set(originNS); err != nil {
		targetNS.Close()
		return netns.None(), fmt.Errorf("restore original netns: %w", err)
	}

	return targetNS, nil
}

func withNamedNetns(name string, fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	originNS, err := netns.Get()
	if err != nil {
		return fmt.Errorf("get current netns: %w", err)
	}
	defer originNS.Close()

	targetNS, err := netns.GetFromName(name)
	if err != nil {
		return fmt.Errorf("open named netns %q: %w", name, err)
	}
	defer targetNS.Close()

	if err := netns.Set(targetNS); err != nil {
		return fmt.Errorf("enter named netns %q: %w", name, err)
	}

	defer func() {
		_ = netns.Set(originNS)
	}()

	return fn()
}
