package platform

import (
	"testing"
)

func validConfig() Config {
	return Config{Kind: "login", DatabaseURL: "postgres://user:password@127.0.0.1:15432/maple_go_test", RedisAddr: "127.0.0.1:16379", Bind: "127.0.0.1", LoginPort: 18484, CashshopPort: 18600, HealthAddr: "127.0.0.1:19080", RPCToken: "01234567890123456789012345678901", Worlds: []WorldConfig{{ID: 0, Host: "127.0.0.1", ChannelCount: 11, ChannelBasePort: 18485, RPCAddr: "127.0.0.1:19485"}}}
}

func TestConfigRejectsAccidentalPublicOrUnrelatedEndpoints(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Config)
	}{
		{"public-bind", func(c *Config) { c.Bind = "0.0.0.0" }},
		{"public-tailnet-bind", func(c *Config) { c.TailnetBind = "192.0.2.10" }},
		{"public-health", func(c *Config) { c.HealthAddr = "0.0.0.0:19080" }},
		{"remote-cache", func(c *Config) { c.RedisAddr = "192.0.2.1:16379" }},
		{"unrelated-database", func(c *Config) { c.DatabaseURL = "postgres://u:p@127.0.0.1:5432/production" }},
		{"remote-world", func(c *Config) { c.Worlds[0].Host = "192.0.2.10" }},
		{"remote-rpc", func(c *Config) { c.Worlds[0].RPCAddr = "192.0.2.10:19485" }},
		{"weak-service-token", func(c *Config) { c.RPCToken = "short" }},
		{"invalid-channels", func(c *Config) { c.Worlds[0].ChannelCount = 21 }},
	}
	if err := validConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.change(&c)
			if c.Validate() == nil {
				t.Fatal("configuration must be rejected")
			}
		})
	}
}

func TestTailnetBindAddsOneListenHost(t *testing.T) {
	c := validConfig()
	if hosts := c.ListenHosts(); len(hosts) != 1 || hosts[0] != "127.0.0.1" {
		t.Fatalf("hosts = %v", hosts)
	}
	c.TailnetBind = "100.64.0.10"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if hosts := c.ListenHosts(); len(hosts) != 2 || hosts[1] != "100.64.0.10" {
		t.Fatalf("hosts = %v", hosts)
	}
}

func TestWorldMustBeConfigured(t *testing.T) {
	c := validConfig()
	c.Kind = "world"
	c.WorldID = 1
	if c.Validate() == nil {
		t.Fatal("unconfigured world accepted")
	}
}

func publicConfig() Config {
	c := validConfig()
	c.Public = true
	c.Bind = "0.0.0.0"
	c.PublicHost = "game.example.com"
	c.DatabaseURL = "postgres://user:password@db.internal:5432/railway"
	c.RedisAddr = "cache.internal:6379"
	c.Worlds[0].Host = "game.example.com"
	c.Worlds[0].RPCAddr = "world.internal:19485"
	return c
}

func TestPublicDeploymentAcceptsRemoteEndpoints(t *testing.T) {
	c := publicConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.PublicHost, c.Worlds[0].Host = "203.0.113.7", "203.0.113.7"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPublicDeploymentRejectsUnusableSettings(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Config)
	}{
		{"no-public-host", func(c *Config) { c.PublicHost = "" }},
		{"loopback-public-host", func(c *Config) { c.PublicHost = "127.0.0.1" }},
		{"unspecified-public-host", func(c *Config) { c.PublicHost = "0.0.0.0" }},
		{"ipv6-public-host", func(c *Config) { c.PublicHost = "2001:db8::1" }},
		{"bare-label-public-host", func(c *Config) { c.PublicHost = "localhost" }},
		{"bind-is-a-name", func(c *Config) { c.Bind = "game.example.com" }},
		{"world-host-loopback", func(c *Config) { c.Worlds[0].Host = "127.0.0.1" }},
		{"rpc-without-port", func(c *Config) { c.Worlds[0].RPCAddr = "world.internal" }},
		{"cache-without-port", func(c *Config) { c.RedisAddr = "cache.internal" }},
		{"database-not-postgres", func(c *Config) { c.DatabaseURL = "mysql://u:p@db.internal/x" }},
		{"public-ports-wrong-count", func(c *Config) { c.Worlds[0].PublicChannelPorts = []int{30001, 30002} }},
		{"public-port-out-of-range", func(c *Config) {
			c.Worlds[0].ChannelCount = 2
			c.Worlds[0].PublicChannelPorts = []int{30001, 70000}
		}},
		{"public-cashshop-port-out-of-range", func(c *Config) { c.PublicCashshopPort = 70000 }},
		{"weak-service-token", func(c *Config) { c.RPCToken = "short" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := publicConfig()
			tc.change(&c)
			if c.Validate() == nil {
				t.Fatal("configuration must be rejected")
			}
		})
	}
}

func TestLocalDeploymentRejectsPublicOnlySettings(t *testing.T) {
	for name, change := range map[string]func(*Config){
		"public-host":          func(c *Config) { c.PublicHost = "game.example.com" },
		"public-cashshop-port": func(c *Config) { c.PublicCashshopPort = 30000 },
		"public-channel-ports": func(c *Config) { c.Worlds[0].PublicChannelPorts = make([]int, 11) },
	} {
		c := validConfig()
		change(&c)
		if c.Validate() == nil {
			t.Errorf("%s accepted without the public deployment mode", name)
		}
	}
}

func TestAdvertisedPortsFollowTheProxyMapping(t *testing.T) {
	c := publicConfig()
	w := c.Worlds[0]
	if w.ChannelPort(3) != 18487 || w.AdvertisedChannelPort(3) != 18487 {
		t.Fatalf("unmapped channel 3: listen %d advertised %d", w.ChannelPort(3), w.AdvertisedChannelPort(3))
	}
	if c.AdvertisedCashshopPort() != 18600 || c.CashshopHost() != "game.example.com" {
		t.Fatalf("cash shop advertised %s:%d", c.CashshopHost(), c.AdvertisedCashshopPort())
	}
	w.ChannelCount, w.PublicChannelPorts = 2, []int{30001, 30002}
	c.Worlds[0], c.PublicCashshopPort = w, 30100
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if w.ChannelPort(2) != 18486 || w.AdvertisedChannelPort(2) != 30002 || c.AdvertisedCashshopPort() != 30100 {
		t.Fatalf("mapped ports: listen %d advertised %d cash shop %d", w.ChannelPort(2), w.AdvertisedChannelPort(2), c.AdvertisedCashshopPort())
	}
	local := validConfig()
	if local.CashshopHost() != "127.0.0.1" {
		t.Fatalf("local cash shop host %q", local.CashshopHost())
	}
}
