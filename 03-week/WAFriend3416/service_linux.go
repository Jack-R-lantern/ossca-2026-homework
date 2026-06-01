//go:build linux

package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
)

type linuxService struct {
	// HTTP 요청은 동시에 들어올 수 있으므로 attach 상태와 BPF map 접근을 mutex로 보호한다.
	mu          sync.Mutex
	objects     *bpfObjects
	attachments map[string]attachedInterface
}

type attachedInterface struct {
	// ifindex는 kernel/XDP program이 보는 interface 식별자다.
	// ifname은 바뀔 수 있지만 checker 시나리오에서는 attach 당시 ifindex로 map key를 구성한다.
	ifindex int
	link    link.Link
}

type blockKey struct {
	// C 코드의 struct block_key와 필드 순서/크기가 같아야 map lookup이 일치한다.
	Ifindex uint32
	SrcIP   [4]byte
}

func newService() service {
	// interface별 attach 상태와 link handle을 서버 프로세스가 유지한다.
	return &linuxService{
		attachments: make(map[string]attachedInterface),
	}
}

func (s *linuxService) Attach(ifname string) (attachResponse, error) {
	if err := validateIfName(ifname); err != nil {
		return attachResponse{}, badRequest(err.Error())
	}

	// 과제에서 interface 생성은 하지 않는다.
	// checker가 미리 만든 host-side veth가 실제로 존재하는지만 확인한다.
	iface, err := net.InterfaceByName(ifname)
	if err != nil {
		return attachResponse{}, badRequest(fmt.Sprintf("lookup interface %q: %v", ifname, err))
	}

	// attach 중 map load/link 생성과 block/clear 요청이 섞이지 않게 보호한다.
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.attachments[ifname]; ok {
		// 같은 interface에 대한 반복 attach는 이미 원하는 상태이므로 성공으로 처리한다.
		return attachResponse{IfName: ifname, Hook: "xdp", Attached: true}, nil
	}

	// 첫 attach 시점에 eBPF object와 map을 한 번만 로드한다.
	if err := s.ensureObjects(); err != nil {
		return attachResponse{}, err
	}

	// 외부 ip/bpftool 명령 없이 netlink 기반 BPF syscall로 XDP에 attach한다.
	xdpLink, err := link.AttachXDP(link.XDPOptions{
		Program:   s.objects.XdpBlock,
		Interface: iface.Index,
	})
	if err != nil {
		return attachResponse{}, fmt.Errorf("attach xdp to %q: %w", ifname, err)
	}

	// link handle을 닫으면 attach가 해제될 수 있으므로 서버가 살아있는 동안 보관한다.
	s.attachments[ifname] = attachedInterface{
		ifindex: iface.Index,
		link:    xdpLink,
	}

	return attachResponse{IfName: ifname, Hook: "xdp", Attached: true}, nil
}

func (s *linuxService) Block(ifname string, ip string) (blockResponse, error) {
	if err := validateIfName(ifname); err != nil {
		return blockResponse{}, badRequest(err.Error())
	}

	// 과제 범위는 IPv4 source IP 차단만 포함하므로 IPv6/CIDR/port는 받지 않는다.
	addr, err := parseIPv4(ip)
	if err != nil {
		return blockResponse{}, badRequest(err.Error())
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	attachment, ok := s.attachments[ifname]
	if !ok {
		// README 요구사항: attach되지 않은 interface에 block 요청이 오면 실패해야 한다.
		return blockResponse{}, badRequest(fmt.Sprintf("interface %q is not attached", ifname))
	}

	// value 자체에는 의미를 두지 않고, key가 map에 존재하는지만 XDP program이 확인한다.
	value := uint8(1)
	// map key에 ifindex를 포함해 interface별 blocked IP가 서로 섞이지 않게 한다.
	key := blockKey{
		Ifindex: uint32(attachment.ifindex),
		SrcIP:   addr.As4(),
	}

	// UpdateAny는 같은 IP를 여러 번 등록해도 최종 상태가 blocked가 되도록 덮어쓴다.
	if err := s.objects.BlockedIps.Update(key, value, ebpf.UpdateAny); err != nil {
		return blockResponse{}, fmt.Errorf("update blocked ip map: %w", err)
	}

	return blockResponse{IfName: ifname, BlockedIP: addr.String()}, nil
}

func (s *linuxService) Clear(ifname string) (clearResponse, error) {
	if err := validateIfName(ifname); err != nil {
		return clearResponse{}, badRequest(err.Error())
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	attachment, ok := s.attachments[ifname]
	if !ok {
		// attach되지 않은 interface는 map 기준 ifindex도 알 수 없으므로 clear도 실패 처리한다.
		return clearResponse{}, badRequest(fmt.Sprintf("interface %q is not attached", ifname))
	}

	// clear는 해당 interface의 key만 찾아 삭제하고, XDP link는 유지한다.
	keys, err := s.keysForInterface(uint32(attachment.ifindex))
	if err != nil {
		return clearResponse{}, err
	}

	for _, key := range keys {
		// 순회 중 이미 지워진 key가 있어도 clear의 목적은 달성된 상태이므로 무시한다.
		if err := s.objects.BlockedIps.Delete(key); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
			return clearResponse{}, fmt.Errorf("delete blocked ip: %w", err)
		}
	}

	return clearResponse{IfName: ifname, Cleared: true}, nil
}

func (s *linuxService) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var closeErr error
	// 서버 종료 시 attach된 XDP link와 BPF object를 정리한다.
	for ifname, attachment := range s.attachments {
		// 첫 오류만 반환하되 가능한 나머지 link 정리도 계속 시도한다.
		if err := attachment.link.Close(); err != nil && closeErr == nil {
			closeErr = fmt.Errorf("close xdp link %q: %w", ifname, err)
		}
	}
	s.attachments = make(map[string]attachedInterface)

	if s.objects != nil {
		if err := s.objects.Close(); err != nil && closeErr == nil {
			closeErr = fmt.Errorf("close bpf objects: %w", err)
		}
		s.objects = nil
	}

	return closeErr
}

func (s *linuxService) ensureObjects() error {
	if s.objects != nil {
		return nil
	}

	// 오래된 커널/환경에서 BPF map 생성이 memlock 제한에 막히지 않게 한다.
	if err := rlimit.RemoveMemlock(); err != nil {
		return fmt.Errorf("remove memlock limit: %w", err)
	}

	objects := &bpfObjects{}
	// loadBpfObjects는 bpf2go가 생성하는 함수이며 C로 작성한 map/program을 kernel에 로드한다.
	if err := loadBpfObjects(objects, nil); err != nil {
		return fmt.Errorf("load bpf objects: %w", err)
	}

	s.objects = objects
	return nil
}

func (s *linuxService) keysForInterface(ifindex uint32) ([]blockKey, error) {
	var (
		key   blockKey
		value uint8
		keys  []blockKey
	)

	iter := s.objects.BlockedIps.Iterate()
	for iter.Next(&key, &value) {
		// map은 모든 interface의 blocked IP를 함께 담으므로 clear 대상 ifindex만 골라낸다.
		if key.Ifindex == ifindex {
			keys = append(keys, key)
		}
	}

	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("iterate blocked ip map: %w", err)
	}

	return keys, nil
}

func parseIPv4(value string) (netip.Addr, error) {
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("ip must be a valid IPv4 address: %w", err)
	}

	// IPv4-mapped IPv6 표현이 들어와도 실제 IPv4 주소면 4바이트 주소로 정규화한다.
	addr = addr.Unmap()
	if !addr.Is4() {
		return netip.Addr{}, fmt.Errorf("ip must be IPv4: %s", value)
	}

	// eBPF 프로그램은 IPv4 source address 4바이트를 그대로 비교한다.
	return addr, nil
}

func validateIfName(name string) error {
	if name == "" {
		return errors.New("interface name is required")
	}

	// Linux IFNAMSIZ는 NUL 포함 16바이트라 사용자에게 보이는 이름은 최대 15자다.
	if len(name) > 15 {
		return errors.New("interface name must be 15 characters or less")
	}

	// interface 이름은 kernel IFNAMSIZ 제한과 경로 삽입 가능성을 함께 막는다.
	if strings.Contains(name, "/") || strings.Contains(name, "\x00") {
		return errors.New("interface name contains invalid characters")
	}

	return nil
}

func badRequest(msg string) error {
	return statusError{status: http.StatusBadRequest, msg: msg}
}
