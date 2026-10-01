package policy

import (
	"fmt"
	"net/netip"
	"testing"
)

// bigEnv is a network of n devices owned by n/4 people.
func bigEnv(n int) Env {
	env := Env{Groups: map[string][]string{"eng": {}}, Overlay: []netip.Prefix{pfx("100.80.0.0/16")}}
	for i := 0; i < n; i++ {
		owner := fmt.Sprintf("u%d", i/4)
		tags := []string(nil)
		if i%10 == 0 {
			tags = []string{"tag:server"}
		}
		env.Nodes = append(env.Nodes, Node{
			ID: fmt.Sprintf("d%d", i), Name: fmt.Sprintf("device-%d", i), OwnerID: owner, OwnerEmail: owner + "@example.com", Tags: tags,
			Addrs: []netip.Prefix{netip.PrefixFrom(netip.AddrFrom4([4]byte{100, 80, byte(i >> 8), byte(i)}), 32)},
		})
		if i%4 == 0 {
			env.Groups["eng"] = append(env.Groups["eng"], owner)
		}
	}
	return env
}

// BenchmarkCompile is the cost of recomputing the whole network's access rules after a change.
func BenchmarkCompile(b *testing.B) {
	p, err := Parse(testPolicy)
	if err != nil {
		b.Fatal(err)
	}
	for _, n := range []int{100, 1000, 5000} {
		env := bigEnv(n)
		b.Run(fmt.Sprintf("%d-devices", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = p.Compile(env)
			}
		})
	}
}

func BenchmarkPeers(b *testing.B) {
	p, err := Parse(testPolicy)
	if err != nil {
		b.Fatal(err)
	}
	c := p.Compile(bigEnv(1000))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = c.Peers("d10")
	}
}
