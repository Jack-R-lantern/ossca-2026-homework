typedef unsigned char __u8;
typedef unsigned short __u16;
typedef unsigned int __u32;

/* libbpf header를 직접 include하지 않고 과제에 필요한 최소 정의만 둔다. */
#define SEC(NAME) __attribute__((section(NAME), used))
#define BPF_MAP_TYPE_HASH 1
#define XDP_DROP 1
#define XDP_PASS 2
#define ETH_P_IP 0x0800

/* bpf2go/libbpf 스타일 map definition에 필요한 helper macro다. */
#define __uint(name, value) int (*name)[value]
#define __type(name, value) typeof(value) *name

/* Ethernet protocol field는 network byte order라 host endian에 맞춰 상수를 변환한다. */
#if __BYTE_ORDER__ == __ORDER_LITTLE_ENDIAN__
#define bpf_htons(value) __builtin_bswap16(value)
#else
#define bpf_htons(value) (value)
#endif

/* XDP hook이 kernel에서 넘겨주는 context 중 이번 과제에 필요한 필드만 정의한다. */
struct xdp_md {
	__u32 data;
	__u32 data_end;
	__u32 data_meta;
	__u32 ingress_ifindex;
	__u32 rx_queue_index;
};

/* XDP program은 raw packet buffer를 직접 읽으므로 Ethernet/IP header 구조체를 최소 정의한다. */
struct ethhdr {
	__u8 h_dest[6];
	__u8 h_source[6];
	__u16 h_proto;
} __attribute__((packed));

struct iphdr {
	__u8 version_ihl;
	__u8 tos;
	__u16 tot_len;
	__u16 id;
	__u16 frag_off;
	__u8 ttl;
	__u8 protocol;
	__u16 check;
	__u8 saddr[4];
	__u8 daddr[4];
} __attribute__((packed));

struct block_key {
	__u32 ifindex;
	__u8 src_ip[4];
};

/*
 * interface별 차단을 위해 ifindex와 IPv4 source address를 함께 key로 사용한다.
 * 같은 source IP라도 다른 veth interface에 등록된 정책에는 영향을 주면 안 되기 때문이다.
 */
struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 4096);
	__type(key, struct block_key);
	__type(value, __u8);
} blocked_ips SEC(".maps");

static void *(*bpf_map_lookup_elem)(void *map, const void *key) = (void *)1;

SEC("xdp")
int xdp_block(struct xdp_md *ctx)
{
	/* data/data_end는 verifier가 packet bounds check를 판단하는 기준이다. */
	void *data = (void *)(long)ctx->data;
	void *data_end = (void *)(long)ctx->data_end;
	struct ethhdr *eth = data;

	/* 패킷이 Ethernet header보다 짧으면 검사하지 않고 통과시킨다. */
	if ((void *)(eth + 1) > data_end)
		return XDP_PASS;

	/* 과제 범위는 IPv4 source IP 차단이므로 IPv4가 아니면 통과시킨다. */
	if (eth->h_proto != bpf_htons(ETH_P_IP))
		return XDP_PASS;

	/* Ethernet header 바로 뒤에 IPv4 header가 온다고 보고, 먼저 최소 IPv4 header 길이를 검사한다. */
	struct iphdr *ip = (void *)(eth + 1);
	if ((void *)(ip + 1) > data_end)
		return XDP_PASS;

	/* version_ihl의 하위 4비트가 IPv4 header length이며, 단위는 4바이트다. */
	__u8 ihl = ip->version_ihl & 0x0f;
	if (ihl < 5)
		return XDP_PASS;

	/* IPv4 option이 붙은 packet도 verifier가 안전하다고 판단하도록 실제 header 끝을 검사한다. */
	if ((void *)((__u8 *)ip + ihl * 4) > data_end)
		return XDP_PASS;

	struct block_key key = {};
	/* Go userspace가 넣은 key와 같은 형태로 현재 ingress ifindex와 source IP를 채운다. */
	key.ifindex = ctx->ingress_ifindex;
	key.src_ip[0] = ip->saddr[0];
	key.src_ip[1] = ip->saddr[1];
	key.src_ip[2] = ip->saddr[2];
	key.src_ip[3] = ip->saddr[3];

	/* map에 key가 있으면 차단 대상이고, 없으면 통과 대상이다. value 내용은 사용하지 않는다. */
	__u8 *blocked = bpf_map_lookup_elem(&blocked_ips, &key);
	if (blocked)
		/* blocked map에 등록된 source IP면 checker의 ping이 실패하도록 drop한다. */
		return XDP_DROP;

	return XDP_PASS;
}

char __license[] SEC("license") = "GPL";
