package domain

import "net"

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
	return configured
}
