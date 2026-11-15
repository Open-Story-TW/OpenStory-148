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
