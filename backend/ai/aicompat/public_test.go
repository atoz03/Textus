package aicompat

import (
	"net/netip"
	"testing"
)

func TestPrivateLoopbackAndSpecialAddressesAreNotPublic(t *testing.T) {
	for _, a := range []string{
		"0.0.0.0", "0.1.2.3", "10.2.3.4", "127.0.0.1", "127.8.9.10", "100.64.0.1", "100.127.255.254", "169.254.169.254",
		"172.16.0.1", "172.31.255.255", "192.0.0.8", "192.0.2.1", "192.88.99.1", "192.168.1.1", "198.18.0.1",
		"198.19.255.255", "198.51.100.7", "203.0.113.9", "224.0.0.1", "239.255.255.250", "240.0.0.1", "255.255.255.255",
		// IPv6 outside global unicast, or carrying a private IPv4 address.
		"::", "::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254", "::ffff:100.64.0.1", "::7f00:1",
		// NAT64, whatever IPv4 address it reaches; 6to4, Teredo,
		// benchmarking, documentation.
		"64:ff9b::a9fe:a9fe", "64:ff9b::808:808", "64:ff9b:1::1", "fc00::1", "fd00::1", "fe80::1", "fec0::1", "ff02::1",
		"100::1", "2002:c0a8:0101::1", "2002:0808:0808::1", "2001::1", "2001:2::1", "2001:db8::1", "3fff::1",
	} {
		if IsPublic(netip.MustParseAddr(a)) {
			t.Errorf("%s counted as public", a)
		}
	}
	for _, a := range []string{
		"1.1.1.1", "8.8.8.8", "93.184.215.14", "100.63.255.255", "100.128.0.1", "172.15.255.255", "172.32.0.1",
		"::ffff:8.8.8.8", "2606:4700:4700::1111", "2a00:1450:4001::1", "2001:4860:4860::8888",
	} {
		if !IsPublic(netip.MustParseAddr(a)) {
			t.Errorf("%s counted as private", a)
		}
	}
}

// DNS rebinding: a public-looking name that answers with a private
// address, among public ones or alone, is refused.
func TestANameIsPublicOnlyIfEveryAddressItResolvesToIs(t *testing.T) {
	ips := func(list ...string) []netip.Addr {
		var out []netip.Addr
		for _, s := range list {
			out = append(out, netip.MustParseAddr(s))
		}
		return out
	}
	if !AllPublic(ips("1.1.1.1", "2606:4700:4700::1111")) || AllPublic(ips("1.1.1.1", "127.0.0.1")) || AllPublic(ips("10.0.0.1")) ||
		AllPublic(ips("2606:4700:4700::1111", "::ffff:192.168.0.1")) || AllPublic(nil) {
		t.Fatal("rebinding judged wrong")
	}
}

func TestLocalNamesAreRecognisedBeforeAnyLookup(t *testing.T) {
	for _, h := range []string{"localhost", "LOCALHOST.", "app.localhost", "printer.local", "metadata.google.internal", "nas.home.arpa"} {
		if !IsLocalName(h) {
			t.Error(h)
		}
	}
	for _, h := range []string{"example.com", "localhost.example.com", "local.example"} {
		if IsLocalName(h) {
			t.Error(h)
		}
	}
}
