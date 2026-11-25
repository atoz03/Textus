package aicompat

import (
	"net/netip"
	"strings"
)

// Whether an address is on the public internet: what the web tools and a
// picture's download may reach, and what they refuse - this computer, the
// local network, and the special-purpose ranges.

// IsPublic reports whether ip is on the public internet. An IPv4-mapped
// IPv6 address is judged as the IPv4 address it carries; otherwise only
// global unicast passes, less the special-purpose and documentation
// ranges.
func IsPublic(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.Is4() {
		o := ip.As4()
		a, b, c := o[0], o[1], o[2]
		return !(a == 0 || a == 10 || a == 127 ||
			(a == 100 && b&0xc0 == 64) ||
			(a == 169 && b == 254) ||
			(a == 172 && b&0xf0 == 16) ||
			(a == 192 && b == 0 && (c == 0 || c == 2)) ||
			(a == 192 && b == 88 && c == 99) ||
			(a == 192 && b == 168) ||
			(a == 198 && b&0xfe == 18) ||
			(a == 198 && b == 51 && c == 100) ||
			(a == 203 && b == 0 && c == 113) ||
			// Multicast 224/4, reserved 240/4 and broadcast.
			a >= 224)
	}
	if !ip.Is6() {
		return false
	}
	o := ip.As16()
	s0, s1 := uint16(o[0])<<8|uint16(o[1]), uint16(o[2])<<8|uint16(o[3])
	return s0&0xe000 == 0x2000 && s0 != 0x2002 &&
		!(s0 == 0x2001 && (s1 < 0x0200 || s1 == 0x0db8)) &&
		s0&0xfff0 != 0x3ff0
}

// AllPublic reports whether every address a name resolved to is public -
// none at all is not. All of them, not the first: a name answering with
// one public and one private address would otherwise reach the private
// one on a retry.
func AllPublic(addrs []netip.Addr) bool {
	for _, ip := range addrs {
		if !IsPublic(ip) {
			return false
		}
	}
	return len(addrs) > 0
}

// IsLocalName reports whether host means this machine or the local
// network, refused before any lookup: a resolver may answer it from the
// hosts file or mDNS, and a proxy resolves it on its own side.
func IsLocalName(host string) bool {
	host = strings.ToLower(strings.TrimRight(host, "."))
	return host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".home.arpa")
}
