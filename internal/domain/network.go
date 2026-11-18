package domain

import (
	"context"
	"net"
	"sync"
	"time"
)

// tailnet is the shared address range Tailscale assigns to its devices.
var tailnet = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// TailnetIP reports whether ip is a Tailscale device address.
func TailnetIP(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.To4() != nil && tailnet.Contains(parsed)
}

// TrustedClientIP reports whether a game client at ip may use the handoff that
// identifies it by address alone: the server's own machine, or a device on the
// operator's tailnet.
func TrustedClientIP(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && (parsed.IsLoopback() || TailnetIP(ip))
}

// AdvertisedHost is the address to send a client that must reconnect. A client
// that reached the server over the tailnet is sent back to that same address;
// every other client gets the configured host.
func AdvertisedHost(local net.Addr, configured string) string {
	if local != nil {
		if host, _, err := net.SplitHostPort(local.String()); err == nil && TailnetIP(host) {
			return host
		}
	}
	return ResolveIPv4(configured)
}

type resolvedHost struct {
	ip string
	at time.Time
}

var resolvedHosts sync.Map

// ResolveIPv4 returns host as an IPv4 address for the reconnect packets, which
// carry four address bytes. An address is returned as it is. A DNS name is
// looked up and the answer kept for half a minute; when a lookup fails the
// last answer is used. It returns "" when the name has never resolved.
func ResolveIPv4(host string) string {
	if net.ParseIP(host) != nil {
		return host
	}
	cached, known := resolvedHosts.Load(host)
	if known && time.Since(cached.(resolvedHost).at) < 30*time.Second {
		return cached.(resolvedHost).ip
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil || len(ips) == 0 {
		if known {
			return cached.(resolvedHost).ip
		}
		return ""
	}
	ip := ips[0].String()
	resolvedHosts.Store(host, resolvedHost{ip: ip, at: time.Now()})
	return ip
}
