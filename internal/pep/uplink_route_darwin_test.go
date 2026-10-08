package pep

import (
	"golang.org/x/net/route"
	"syscall"
	"testing"
)

func TestUplinkGatewayIgnoresOtherAddressFamily(t *testing.T) {
	v4 := &route.RouteMessage{Addrs: make([]route.Addr, syscall.RTAX_MAX)}
	v4.Addrs[syscall.RTAX_DST] = &route.Inet4Addr{}
	v4.Addrs[syscall.RTAX_GATEWAY] = &route.Inet4Addr{IP: [4]byte{192, 168, 1, 1}}
	v6 := &route.RouteMessage{Addrs: make([]route.Addr, syscall.RTAX_MAX)}
	v6.Addrs[syscall.RTAX_DST] = &route.Inet6Addr{}
	v6.Addrs[syscall.RTAX_GATEWAY] = &route.Inet6Addr{IP: [16]byte{0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}}
	for _, test := range []struct {
		name, source string
		message      *route.RouteMessage
		want         string
	}{
		{"ipv4 keeps ipv4", "192.168.1.2", v4, "192.168.1.1"},
		{"ipv4 ignores ipv6", "192.168.1.2", v6, ""},
		{"scoped ipv6 ignores ipv4", "fe80::2%en0", v4, ""},
		{"ipv6 ignores ipv4", "2001:db8::2", v4, ""},
		{"ipv6 keeps ipv6", "2001:db8::2", v6, "fe80::1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := defaultRouteGateway(test.message, test.source); got != test.want {
				t.Fatalf("gateway=%q want %q", got, test.want)
			}
		})
	}
}

func TestUplinkAddressIgnoresOtherFamily(t *testing.T) {
	m := &route.InterfaceAddrMessage{Addrs: make([]route.Addr, syscall.RTAX_MAX)}
	m.Addrs[syscall.RTAX_IFA] = &route.Inet6Addr{IP: [16]byte{0x20, 1, 0xd, 0xb8}}
	if uplinkAddressChanged(m, "192.168.1.2") {
		t.Fatal("IPv6 address churn reset an IPv4 pool")
	}
	if !uplinkAddressChanged(m, "2001:db8::2") {
		t.Fatal("lost IPv6 address event")
	}
	m.Addrs[syscall.RTAX_IFA] = &route.Inet4Addr{IP: [4]byte{192, 168, 1, 3}}
	if !uplinkAddressChanged(m, "192.168.1.2") {
		t.Fatal("lost IPv4 address event")
	}
	if uplinkAddressChanged(m, "2001:db8::2") {
		t.Fatal("IPv4 address churn reset an IPv6 pool")
	}
}

func TestHealthyUplinkNotificationsDoNotInterruptConnections(t *testing.T) {
	link := &route.InterfaceMessage{Flags: syscall.IFF_UP | syscall.IFF_RUNNING}
	if uplinkInterrupted(link, "192.168.1.2", true) {
		t.Fatal("initial healthy interface report resets the pool")
	}
	link.Flags |= syscall.IFF_PROMISC
	if uplinkInterrupted(link, "192.168.1.2", true) {
		t.Fatal("unrelated interface flag resets the pool")
	}
	link.Flags &^= syscall.IFF_RUNNING
	if !uplinkInterrupted(link, "192.168.1.2", true) {
		t.Fatal("missed a physical link interruption")
	}
	m := &route.RouteMessage{Flags: syscall.RTF_IFSCOPE, Addrs: make([]route.Addr, syscall.RTAX_MAX)}
	m.Addrs[syscall.RTAX_DST] = &route.Inet4Addr{}
	m.Addrs[syscall.RTAX_GATEWAY] = &route.Inet4Addr{IP: [4]byte{192, 168, 1, 1}}
	for _, kind := range []int{syscall.RTM_ADD, syscall.RTM_CHANGE} {
		m.Type = kind
		if uplinkInterrupted(m, "192.168.1.2", true) {
			t.Fatal("refreshing the same default route resets the pool")
		}
	}
	m.Type = syscall.RTM_DELETE
	if !uplinkInterrupted(m, "192.168.1.2", true) {
		t.Fatal("missed loss of the bound default route")
	}
	address := &route.InterfaceAddrMessage{Type: syscall.RTM_DELADDR, Addrs: make([]route.Addr, syscall.RTAX_MAX)}
	address.Addrs[syscall.RTAX_IFA] = &route.Inet4Addr{IP: [4]byte{192, 168, 1, 3}}
	if uplinkInterrupted(address, "192.168.1.2", true) {
		t.Fatal("removing another address resets the bound pool")
	}
	address.Addrs[syscall.RTAX_IFA] = &route.Inet4Addr{IP: [4]byte{192, 168, 1, 2}}
	if !uplinkInterrupted(address, "192.168.1.2", true) {
		t.Fatal("missed loss of the socket's source address")
	}
}
