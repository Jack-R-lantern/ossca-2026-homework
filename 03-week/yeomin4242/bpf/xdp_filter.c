typedef unsigned char __u8;
typedef unsigned short __u16;
typedef unsigned int __u32;

#define SEC(NAME) __attribute__((section(NAME), used))
#define __uint(name, val) int (*name)[val]
#define __type(name, val) typeof(val) *name

#define BPF_MAP_TYPE_HASH 1
#define XDP_PASS 2
#define XDP_DROP 1
#define ETH_P_IP 0x0800

struct xdp_md {
	__u32 data;
	__u32 data_end;
	__u32 data_meta;
	__u32 ingress_ifindex;
	__u32 rx_queue_index;
	__u32 egress_ifindex;
};

struct ethhdr {
	__u8 h_dest[6];
	__u8 h_source[6];
	__u16 h_proto;
};

struct ipv4hdr {
	__u8 version_ihl;
	__u8 tos;
	__u16 total_length;
	__u16 id;
	__u16 frag_offset;
	__u8 ttl;
	__u8 protocol;
	__u16 checksum;
	__u32 source;
	__u32 destination;
};

struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 4096);
	__type(key, __u32);
	__type(value, __u8);
} blocked_ips SEC(".maps");

static void *(*bpf_map_lookup_elem)(void *map, const void *key) = (void *)1;

SEC("xdp")
int xdp_block_ipv4(struct xdp_md *ctx)
{
	void *data = (void *)(long)ctx->data;
	void *data_end = (void *)(long)ctx->data_end;

	struct ethhdr *eth = data;
	if ((void *)(eth + 1) > data_end) {
		return XDP_PASS;
	}

	if (eth->h_proto != __builtin_bswap16(ETH_P_IP)) {
		return XDP_PASS;
	}

	struct ipv4hdr *ip = (void *)(eth + 1);
	if ((void *)(ip + 1) > data_end) {
		return XDP_PASS;
	}

	if ((ip->version_ihl >> 4) != 4) {
		return XDP_PASS;
	}

	__u32 source = __builtin_bswap32(ip->source);
	__u8 *blocked = bpf_map_lookup_elem(&blocked_ips, &source);
	if (blocked != 0) {
		return XDP_DROP;
	}

	return XDP_PASS;
}

char __license[] SEC("license") = "GPL";
