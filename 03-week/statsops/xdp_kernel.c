//go:build ignore

#include <linux/bpf.h>
#include <linux/in.h>
#include <linux/if_ether.h>
#include <linux/ip.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

struct blocked_key {
    __u32 ifindex;
    __u32 ip;
};

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, struct blocked_key);
    __type(value, __u32);
    __uint(max_entries, 10240);
} blocked_ips SEC(".maps");

SEC("xdp")
int xdp_firewall(struct xdp_md *ctx) {
    // begin and end pointers
    void *data_end = (void *)(long)ctx->data_end;
    void *data = (void *)(long)ctx->data;

    struct ethhdr *eth = data;
    if ((void *)(eth + 1) > data_end) {
        return XDP_PASS;
    }

    if (eth->h_proto != bpf_htons(ETH_P_IP)) {
        return XDP_PASS;
    }

    struct iphdr *iph = (void *)(eth + 1);
    if ((void *)(iph + 1) > data_end) {
        return XDP_PASS;
    }


    // Map 조회 Key 생성
    struct blocked_key key = {
        .ifindex = ctx->ingress_ifindex,
        .ip = bpf_ntohl(iph->saddr)
    };

    // Map 조회
    __u32 *blocked = bpf_map_lookup_elem(&blocked_ips, &key);
    if (blocked) {
        return XDP_DROP;
    }
    return XDP_PASS;

}

char _license[] SEC("license") = "GPL";