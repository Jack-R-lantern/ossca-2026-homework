package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/netip"
	"os"
	"strconv"
)

const defaultServerURL = "http://127.0.0.1:8080"

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("checker", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	cfg := scenarioConfig{
		ServerURL:      defaultServerURL,
		NamespaceName:  fmt.Sprintf("xdp-check-%d", os.Getpid()),
		HostIfName:     fmt.Sprintf("vxdp%d", os.Getpid()%10000),
		PeerIfName:     "eth0",
		HostCIDR:       "10.20.0.1/24",
		PeerCIDR:       "10.20.0.2/24",
		PingCount:      2,
		PingTimeoutSec: 1,
	}

	fs.StringVar(&cfg.ServerURL, "server-url", cfg.ServerURL, "HTTP server base URL")
	fs.StringVar(&cfg.NamespaceName, "namespace", cfg.NamespaceName, "named network namespace for the test")
	fs.StringVar(&cfg.HostIfName, "host-ifname", cfg.HostIfName, "host-side veth interface name")
	fs.StringVar(&cfg.PeerIfName, "peer-ifname", cfg.PeerIfName, "peer-side veth interface name inside the namespace")
	fs.StringVar(&cfg.HostCIDR, "host-ip", cfg.HostCIDR, "host-side IPv4 address in CIDR notation")
	fs.StringVar(&cfg.PeerCIDR, "peer-ip", cfg.PeerCIDR, "peer-side IPv4 address in CIDR notation")
	fs.IntVar(&cfg.PingCount, "ping-count", cfg.PingCount, "number of ping packets to send per check")
	fs.IntVar(&cfg.PingTimeoutSec, "ping-timeout", cfg.PingTimeoutSec, "ping timeout in seconds")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: %s [flags]\n", os.Args[0])
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() != 0 {
		fs.Usage()
		return errors.New("unexpected positional arguments")
	}

	if err := cfg.Validate(); err != nil {
		return err
	}

	if err := runScenario(cfg); err != nil {
		return err
	}

	fmt.Printf(
		"xdp scenario verified: namespace=%s host=%s(%s) peer=%s(%s) server=%s\n",
		cfg.NamespaceName,
		cfg.HostIfName,
		cfg.HostCIDR,
		cfg.PeerIfName,
		cfg.PeerCIDR,
		cfg.ServerURL,
	)
	return nil
}

type scenarioConfig struct {
	ServerURL      string
	NamespaceName  string
	HostIfName     string
	PeerIfName     string
	HostCIDR       string
	PeerCIDR       string
	HostAddr       netip.Addr
	PeerAddr       netip.Addr
	PingCount      int
	PingTimeoutSec int
}

func (c *scenarioConfig) Validate() error {
	if c.ServerURL == "" {
		return errors.New("server-url is required")
	}

	if c.NamespaceName == "" {
		return errors.New("namespace is required")
	}

	if c.HostIfName == "" {
		return errors.New("host-ifname is required")
	}

	if c.PeerIfName == "" {
		return errors.New("peer-ifname is required")
	}

	hostPrefix, err := netip.ParsePrefix(c.HostCIDR)
	if err != nil {
		return fmt.Errorf("parse host-ip %q: %w", c.HostCIDR, err)
	}

	peerPrefix, err := netip.ParsePrefix(c.PeerCIDR)
	if err != nil {
		return fmt.Errorf("parse peer-ip %q: %w", c.PeerCIDR, err)
	}

	c.HostAddr = hostPrefix.Addr().Unmap()
	c.PeerAddr = peerPrefix.Addr().Unmap()

	if !c.HostAddr.Is4() {
		return fmt.Errorf("host-ip must be IPv4: %s", c.HostCIDR)
	}

	if !c.PeerAddr.Is4() {
		return fmt.Errorf("peer-ip must be IPv4: %s", c.PeerCIDR)
	}

	if c.PingCount <= 0 {
		return errors.New("ping-count must be greater than zero")
	}

	if c.PingTimeoutSec <= 0 {
		return errors.New("ping-timeout must be greater than zero")
	}

	return nil
}

func (c scenarioConfig) PingTimeoutArg() string {
	return strconv.Itoa(c.PingTimeoutSec)
}
