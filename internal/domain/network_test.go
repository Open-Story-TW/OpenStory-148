package domain

import (
	"net"
	"testing"
)

func TestTrustedClientIPIsLoopbackOrTailnet(t *testing.T) {
	for ip, want := range map[string]bool{
		"127.0.0.1":       true,
		"::1":             true,
		"100.64.0.1":      true,
		"100.64.0.10":     true,
		"100.127.255.254": true,
		"100.63.255.255":  false,
		"100.128.0.1":     false,
		"192.168.1.10":    false,
		"203.0.113.7":     false,
		"":                false,
		"not-an-address":  false,
	} {
		if got := TrustedClientIP(ip); got != want {
			t.Errorf("TrustedClientIP(%q) = %v, want %v", ip, got, want)
		}
	}
}

func TestAdvertisedHostFollowsTheTailnetListener(t *testing.T) {
	tailnet := &net.TCPAddr{IP: net.ParseIP("100.64.0.10"), Port: 18484}
	if got := AdvertisedHost(tailnet, "127.0.0.1"); got != "100.64.0.10" {
		t.Errorf("tailnet listener advertised %q", got)
	}
	loopback := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 18484}
	if got := AdvertisedHost(loopback, "127.0.0.1"); got != "127.0.0.1" {
		t.Errorf("loopback listener advertised %q", got)
	}
	if got := AdvertisedHost(nil, "127.0.0.1"); got != "127.0.0.1" {
		t.Errorf("missing address advertised %q", got)
	}
}
