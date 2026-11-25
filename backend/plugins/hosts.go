package plugins

import (
	"net/url"
	"strconv"
	"strings"
)

// HostAllowed is whether a `network:` grant covers host (and port, 0 for
// none): the host itself, `host:port`, `*.domain` for its subdomains, or
// `*` for any.
func HostAllowed(granted []string, host string, port int) bool {
	host = strings.ToLower(host)
	for _, g := range granted {
		rule, ok := strings.CutPrefix(g, "network:")
		if !ok {
			continue
		}
		rule = strings.ToLower(rule)
		if rule == "*" {
			return true
		}
		ruleHost, rulePort := rule, -1
		if i := strings.LastIndexByte(rule, ':'); i >= 0 {
			ruleHost = rule[:i]
			if p, err := strconv.ParseUint(rule[i+1:], 10, 16); err == nil {
				rulePort = int(p)
			}
		}
		if rulePort >= 0 && rulePort != port {
			continue
		}
		if domain, ok := strings.CutPrefix(ruleHost, "*."); ok {
			if host == domain || strings.HasSuffix(host, "."+domain) {
				return true
			}
		} else if host == ruleHost {
			return true
		}
	}
	return false
}

// isThisComputer: plain HTTP is allowed to this computer only (Zotero's
// local API).
func isThisComputer(host string) bool {
	switch host {
	case "localhost", "127.0.0.1", "[::1]", "::1":
		return true
	}
	return false
}

// portOf is u's port, or its scheme's known one; 0 for neither.
func portOf(u *url.URL) int {
	if p, err := strconv.Atoi(u.Port()); err == nil {
		return p
	}
	switch u.Scheme {
	case "https":
		return 443
	case "http":
		return 80
	}
	return 0
}

// URLAllowed is whether the plugin may fetch u: a granted host over HTTPS,
// or plain HTTP to this computer.
func URLAllowed(granted []string, u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isThisComputer(host) {
			return false
		}
	default:
		return false
	}
	return HostAllowed(granted, host, portOf(u))
}
