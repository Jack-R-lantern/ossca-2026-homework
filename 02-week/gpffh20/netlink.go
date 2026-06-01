//go:build linux

package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	iflaInfoKind uint16 = 1
	iflaInfoData uint16 = 2

	vethInfoPeer uint16 = 1

	iflaNetNsFD uint16 = 28
)

type nlSock struct{ fd int }

func newNLSock() (*nlSock, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return nil, fmt.Errorf("socket(AF_NETLINK, SOCK_RAW, NETLINK_ROUTE): %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("bind netlink socket: %w", err)
	}
	return &nlSock{fd: fd}, nil
}

func (s *nlSock) Close() { unix.Close(s.fd) }

func (s *nlSock) do(req []byte) error {
	if err := unix.Send(s.fd, req, 0); err != nil {
		return fmt.Errorf("netlink send: %w", err)
	}
	buf := make([]byte, 4096)
	for {
		n, _, err := unix.Recvfrom(s.fd, buf, 0)
		if err != nil {
			return fmt.Errorf("netlink recv: %w", err)
		}
		if n < unix.SizeofNlMsghdr {
			return fmt.Errorf("netlink response too short (%d bytes)", n)
		}
		switch binary.NativeEndian.Uint16(buf[4:6]) {
		case unix.NLMSG_ERROR:
			if n < unix.SizeofNlMsghdr+4 {
				return fmt.Errorf("NLMSG_ERROR payload truncated")
			}
			code := int32(binary.NativeEndian.Uint32(buf[unix.SizeofNlMsghdr:]))
			if code != 0 {
				return syscall.Errno(-code)
			}
			return nil
		case unix.NLMSG_DONE:
			return nil
		}
	}
}

func (s *nlSock) getIndex(name string) (int32, error) {
	body := concat(
		makeIfInfomsg(unix.AF_UNSPEC, 0, 0, 0),
		nlAttrStr(unix.IFLA_IFNAME, name),
	)
	msg := buildMsg(unix.RTM_GETLINK, unix.NLM_F_REQUEST, body)

	if err := unix.Send(s.fd, msg, 0); err != nil {
		return 0, fmt.Errorf("send RTM_GETLINK(%s): %w", name, err)
	}
	buf := make([]byte, 8192)
	n, _, err := unix.Recvfrom(s.fd, buf, 0)
	if err != nil {
		return 0, fmt.Errorf("recv RTM_GETLINK(%s): %w", name, err)
	}
	if n < unix.SizeofNlMsghdr {
		return 0, fmt.Errorf("RTM_GETLINK(%s): response too short", name)
	}
	if binary.NativeEndian.Uint16(buf[4:6]) == unix.NLMSG_ERROR {
		code := int32(binary.NativeEndian.Uint32(buf[unix.SizeofNlMsghdr:]))
		return 0, fmt.Errorf("RTM_GETLINK(%s): %w", name, syscall.Errno(-code))
	}
	const idxOff = unix.SizeofNlMsghdr + 4
	if n < idxOff+4 {
		return 0, fmt.Errorf("RTM_GETLINK(%s): IfInfomsg truncated", name)
	}
	return int32(binary.NativeEndian.Uint32(buf[idxOff:])), nil
}

func (s *nlSock) addVethPair(hostName, peerName string) error {
	peerDesc := concat(
		makeIfInfomsg(unix.AF_UNSPEC, 0, 0, 0),
		nlAttrStr(unix.IFLA_IFNAME, peerName),
	)
	body := concat(
		makeIfInfomsg(unix.AF_UNSPEC, 0, 0, 0),
		nlAttrStr(unix.IFLA_IFNAME, hostName),
		nlAttrNested(unix.IFLA_LINKINFO,
			nlAttrStr(iflaInfoKind, "veth"),
			nlAttrNested(iflaInfoData,
				nlAttr(vethInfoPeer, peerDesc),
			),
		),
	)
	msg := buildMsg(unix.RTM_NEWLINK,
		unix.NLM_F_REQUEST|unix.NLM_F_ACK|unix.NLM_F_CREATE|unix.NLM_F_EXCL,
		body)
	if err := s.do(msg); err != nil {
		return fmt.Errorf("addVethPair(%s, %s): %w", hostName, peerName, err)
	}
	return nil
}

func (s *nlSock) setLinkNsFD(idx int32, nsFD int) error {
	body := concat(
		makeIfInfomsg(unix.AF_UNSPEC, uint32(idx), 0, 0),
		nlAttrU32(iflaNetNsFD, uint32(nsFD)),
	)
	msg := buildMsg(unix.RTM_NEWLINK, unix.NLM_F_REQUEST|unix.NLM_F_ACK, body)
	if err := s.do(msg); err != nil {
		return fmt.Errorf("setLinkNsFD(idx=%d): %w", idx, err)
	}
	return nil
}

func (s *nlSock) renameLink(idx int32, newName string) error {
	body := concat(
		makeIfInfomsg(unix.AF_UNSPEC, uint32(idx), 0, 0),
		nlAttrStr(unix.IFLA_IFNAME, newName),
	)
	msg := buildMsg(unix.RTM_NEWLINK, unix.NLM_F_REQUEST|unix.NLM_F_ACK, body)
	if err := s.do(msg); err != nil {
		return fmt.Errorf("renameLink(idx=%d → %s): %w", idx, newName, err)
	}
	return nil
}

func (s *nlSock) setLinkUp(idx int32) error {
	body := makeIfInfomsg(unix.AF_UNSPEC, uint32(idx), unix.IFF_UP, unix.IFF_UP)
	msg := buildMsg(unix.RTM_NEWLINK, unix.NLM_F_REQUEST|unix.NLM_F_ACK, body)
	if err := s.do(msg); err != nil {
		return fmt.Errorf("setLinkUp(idx=%d): %w", idx, err)
	}
	return nil
}

func (s *nlSock) addIPv4Addr(idx int32, cidr string) error {
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("parse CIDR %q: %w", cidr, err)
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return fmt.Errorf("not an IPv4 address: %s", cidr)
	}
	prefixLen, _ := ipNet.Mask.Size()

	ifam := [8]byte{
		unix.AF_INET,
		uint8(prefixLen),
		0,
		unix.RT_SCOPE_UNIVERSE,
	}
	binary.NativeEndian.PutUint32(ifam[4:], uint32(idx))

	body := concat(
		ifam[:],
		nlAttr(unix.IFA_LOCAL, ip4),
		nlAttr(unix.IFA_ADDRESS, ip4),
	)
	msg := buildMsg(unix.RTM_NEWADDR,
		unix.NLM_F_REQUEST|unix.NLM_F_ACK|unix.NLM_F_CREATE|unix.NLM_F_EXCL,
		body)
	if err := s.do(msg); err != nil {
		return fmt.Errorf("addIPv4Addr(idx=%d, %s): %w", idx, cidr, err)
	}
	return nil
}

func (s *nlSock) deleteLink(name string) error {
	idx, err := s.getIndex(name)
	if err != nil {
		return err
	}
	body := makeIfInfomsg(unix.AF_UNSPEC, uint32(idx), 0, 0)
	msg := buildMsg(unix.RTM_DELLINK, unix.NLM_F_REQUEST|unix.NLM_F_ACK, body)
	return s.do(msg)
}

func createVeth(nsName string, req createVethReq) error {
	nsPath := filepath.Join(netnsDir, nsName)
	tmpPeer := tempPeerName(req.HostIfname)

	nl, err := newNLSock()
	if err != nil {
		return err
	}
	defer nl.Close()

	if err := nl.addVethPair(req.HostIfname, tmpPeer); err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = nl.deleteLink(req.HostIfname)
		}
	}()

	peerIdx, err := nl.getIndex(tmpPeer)
	if err != nil {
		return fmt.Errorf("getIndex(%s): %w", tmpPeer, err)
	}

	nsFD, err := unix.Open(nsPath, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open namespace %s: %w", nsPath, err)
	}
	defer unix.Close(nsFD)

	if err := nl.setLinkNsFD(peerIdx, nsFD); err != nil {
		return err
	}

	hostIdx, err := nl.getIndex(req.HostIfname)
	if err != nil {
		return fmt.Errorf("getIndex(%s): %w", req.HostIfname, err)
	}
	if err := nl.addIPv4Addr(hostIdx, req.HostIP); err != nil {
		return err
	}
	if err := nl.setLinkUp(hostIdx); err != nil {
		return err
	}

	if err := withNetNS(nsPath, func() error {
		nsnl, err := newNLSock()
		if err != nil {
			return err
		}
		defer nsnl.Close()

		idx, err := nsnl.getIndex(tmpPeer)
		if err != nil {
			return fmt.Errorf("getIndex(%s) in ns: %w", tmpPeer, err)
		}
		if err := nsnl.renameLink(idx, req.PeerIfname); err != nil {
			return err
		}

		pIdx, err := nsnl.getIndex(req.PeerIfname)
		if err != nil {
			return fmt.Errorf("getIndex(%s) after rename: %w", req.PeerIfname, err)
		}
		if err := nsnl.addIPv4Addr(pIdx, req.PeerIP); err != nil {
			return err
		}
		if err := nsnl.setLinkUp(pIdx); err != nil {
			return err
		}

		loIdx, err := nsnl.getIndex("lo")
		if err != nil {
			return fmt.Errorf("getIndex(lo): %w", err)
		}
		return nsnl.setLinkUp(loIdx)
	}); err != nil {
		return fmt.Errorf("configure peer in namespace: %w", err)
	}

	cleanup = false
	return nil
}

func nlAlign(n int) int { return (n + 3) &^ 3 }

func nlAttr(typ uint16, data []byte) []byte {
	hdrLen := 4
	rawLen := hdrLen + len(data)
	buf := make([]byte, nlAlign(rawLen))
	binary.NativeEndian.PutUint16(buf[0:], uint16(rawLen))
	binary.NativeEndian.PutUint16(buf[2:], typ)
	copy(buf[4:], data)
	return buf
}

func nlAttrStr(typ uint16, s string) []byte {
	return nlAttr(typ, append([]byte(s), 0))
}

func nlAttrU32(typ uint16, v uint32) []byte {
	b := make([]byte, 4)
	binary.NativeEndian.PutUint32(b, v)
	return nlAttr(typ, b)
}

func nlAttrNested(typ uint16, children ...[]byte) []byte {
	return nlAttr(typ, concat(children...))
}

func buildMsg(msgType, flags uint16, body []byte) []byte {
	hdrLen := unix.SizeofNlMsghdr
	totalLen := hdrLen + len(body)
	buf := make([]byte, nlAlign(totalLen))
	binary.NativeEndian.PutUint32(buf[0:], uint32(totalLen))
	binary.NativeEndian.PutUint16(buf[4:], msgType)
	binary.NativeEndian.PutUint16(buf[6:], flags)
	copy(buf[hdrLen:], body)
	return buf
}

func makeIfInfomsg(family byte, index, flags, change uint32) []byte {
	buf := make([]byte, unix.SizeofIfInfomsg)
	buf[0] = family
	binary.NativeEndian.PutUint32(buf[4:], index)
	binary.NativeEndian.PutUint32(buf[8:], flags)
	binary.NativeEndian.PutUint32(buf[12:], change)
	return buf
}

func concat(parts ...[]byte) []byte {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func tempPeerName(hostIfname string) string {
	h := uint32(2166136261)
	for _, b := range []byte(hostIfname) {
		h ^= uint32(b)
		h *= 16777619
	}
	return fmt.Sprintf("tmp%08x", h)
}
