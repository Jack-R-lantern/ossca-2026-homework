# 03주차 과제

## 학습 내용
* eBPF가 무엇인지
* eBPF가 어떻게 동작하는지
* XDP Hook에서 Packet을 처리하는 방식
* BPF Map을 통해 Userspace와 Kernel Program이 상태를 공유하는 방식

## 목표

이번 과제에서는 추가한 veth에 BPF Program을 Attach 합니다.

host network namespace에 존재하는 특정 veth interface에 XDP 기반 eBPF program을 attach하고, HTTP API를 통해 interface별로 차단할 IPv4 주소를 등록하거나 flush 할 수 있어야 한다.

이번 과제는 다음 내용을 검증한다.
* 특정 interface에 XDP program attach 여부
* interface별 blocked IP 관리 여부
* blocked IP에 해당하는 packet drop 여부
* clear 요청 이후 blocked IP flush 여부

다음은 구현하지 않는다.
* tc hook 사용
* ingress/egress qdisc 설정
* IPv6 filtering
* CIDR, port, protocol 단위 정책 관리
* interface 생성 자체

## 구현 요구사항
1. 서버는 8080 port로 Listen 한다.
2. XDP program attach
   1. 서버는 `/bpf/attach` URL path를 제공해야 한다.
   2. `/bpf/attach`는 POST 요청을 받는다.
   3. Request body는 JSON 형식이며 다음 필드를 가진다.
		```json
		{
			"ifname": "veth-test01"
		}
		```
	* ifname은 XDP program을 attach할 host network namespace의 veth interface 이름이다.
   4. `/bpf/attach`는 요청을 받으면 해당 interface에 XDP 기반 eBPF program을 attach해야 한다.
   5. attach 대상 interface는 이미 존재하는 interface여야 한다.
   6. `ip link`, `bpftool` 같은 외부 명령어를 프로그램 내부에서 호출하면 안 된다.
   7. attach 이후 해당 interface로 ingress 되는 packet은 XDP program을 통과해야 한다.
   8. API response는 JSON 형식으로 다음 값을 반환해야 한다.
		```json
		{
			"ifname": "veth-test01",
			"hook": "xdp",
			"attached": true
		}
		```
3. blocked IP 등록
   1. 서버는 `/bpf/block/{ifname}` URL path를 제공해야 한다.
   2. `/bpf/block/{ifname}`는 POST 요청을 받는다.
   3. Path parameter의 ifname은 blocked IP를 적용할 veth interface 이름이다.
   4. Request body는 JSON 형식이며 다음 필드를 가진다.
		```json
		{
			"ip": "10.10.0.2"
		}
		```
	* ip는 차단할 IPv4 address이다.
   5. `/bpf/block/{ifname}`는 요청을 받으면 해당 interface에 attach된 XDP program이 참조하는 BPF map 또는 동등한 kernel-side state에 blocked IP를 등록해야 한다.
   6. XDP program은 해당 interface로 ingress 되는 IPv4 packet 중 source IP가 등록된 blocked IP와 일치하면 `XDP_DROP` 해야 한다.
   7. attach 되지 않은 interface에 대해 `/bpf/block/{ifname}`를 호출한 경우 요청은 실패해야 한다.
   8. 특정 interface에 등록한 blocked IP는 다른 interface의 차단 규칙에 영향을 주면 안 된다.
   9. API response는 JSON 형식으로 다음 값을 반환해야 한다.
		```json
		{
			"ifname": "veth-test01",
			"blocked_ip": "10.10.0.2"
		}
		```
4. blocked IP flush
   1. 서버는 `/bpf/clear/{ifname}` URL path를 제공해야 한다.
   2. `/bpf/clear/{ifname}`는 POST 요청을 받는다.
   3. Path parameter의 ifname은 blocked IP를 flush할 대상 veth interface 이름이다.
   4. `/bpf/clear/{ifname}`는 요청을 받으면 해당 interface의 XDP program이 사용하던 blocked IP 목록을 모두 비워야 한다.
   5. clear는 blocked IP 목록만 flush 해야 하며, XDP program attach 자체를 제거하면 안 된다.
   6. clear 이후에는 이전에 차단되던 IPv4 packet이 더 이상 blocked IP 목록 때문에 drop 되면 안 된다.
   7. 특정 interface에 대한 clear는 다른 interface의 blocked IP 목록에 영향을 주면 안 된다.
   8. API response는 JSON 형식으로 다음 값을 반환해야 한다.
		```json
		{
			"ifname": "veth-test01",
			"cleared": true
		}
		```
5. 실행 환경
   1. 본 과제는 root 권한 또는 eBPF/XDP attach가 가능한 Linux 환경에서 실행한다고 가정한다.
   2. XDP program은 최소한 IPv4 packet을 식별하고 `XDP_PASS`, `XDP_DROP`를 반환할 수 있어야 한다.
   3. userspace 프로그램과 kernel eBPF program 사이의 blocked IP 동기화가 올바르게 동작해야 한다.
