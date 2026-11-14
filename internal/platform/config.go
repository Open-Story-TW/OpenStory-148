package platform

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type WorldConfig struct {
	ID              int    `json:"id"`
	Name            string `json:"name"`
	Host            string `json:"host"`
	ChannelBasePort int    `json:"channel_base_port"`
	ChannelCount    int    `json:"channel_count"`
	RPCAddr         string `json:"rpc_addr"`
}

type Config struct {
	Kind                string
	InstanceID          string
	DatabaseURL         string
	RedisAddr           string
	RedisPassword       string
	RedisDB             int
	Bind                string
	LoginPort           int
	CashshopPort        int
	HealthAddr          string
	WorldID             int
	Worlds              []WorldConfig
	RPCToken            string
	StopFile            string
	AllowLegacyLoopback bool
	// ScriptsDir, when set, is a directory of Lua sources laid over the ones
	// built into the world service; see scripts.Prepare.
	ScriptsDir string
}

func value(name, fallback string) string {
	if s := os.Getenv(name); s != "" {
		return s
	}
	return fallback
}

func integer(name string, fallback int) (int, error) {
	s := os.Getenv(name)
	if s == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return v, nil
}

// Load accepts only isolated loopback development endpoints. Production network
// exposure requires a client handoff credential with a reviewed security contract.
func Load(kind string) (Config, error) {
	c := Config{Kind: kind, InstanceID: value("MAPLEGO_INSTANCE_ID", NewID()),
		DatabaseURL: os.Getenv("MAPLEGO_DATABASE_URL"), RedisAddr: value("MAPLEGO_REDIS_ADDR", "127.0.0.1:16379"),
		RedisPassword: os.Getenv("MAPLEGO_REDIS_PASSWORD"), Bind: value("MAPLEGO_BIND", "127.0.0.1"),
		RPCToken: os.Getenv("MAPLEGO_RPC_TOKEN"), StopFile: os.Getenv("MAPLEGO_STOP_FILE"), ScriptsDir: os.Getenv("MAPLEGO_SCRIPTS_DIR"),
		AllowLegacyLoopback: value("MAPLEGO_LEGACY_LOOPBACK", "true") == "true"}
	var err error
	for _, entry := range []struct {
		name     string
		dst      *int
		fallback int
	}{
		{"MAPLEGO_LOGIN_PORT", &c.LoginPort, 18484}, {"MAPLEGO_CASHSHOP_PORT", &c.CashshopPort, 18600},
		{"MAPLEGO_WORLD_ID", &c.WorldID, 0}, {"MAPLEGO_REDIS_DB", &c.RedisDB, 0},
	} {
		*entry.dst, err = integer(entry.name, entry.fallback)
		if err != nil {
			return c, err
		}
	}
	base, count := 18485, 11
	if base, err = integer("MAPLEGO_CHANNEL_BASE_PORT", base); err != nil {
		return c, err
	}
	if count, err = integer("MAPLEGO_CHANNEL_COUNT", count); err != nil {
		return c, err
	}
	c.Worlds = []WorldConfig{{ID: 0, Name: "Go 雪吉拉", Host: c.Bind, ChannelBasePort: base, ChannelCount: count, RPCAddr: value("MAPLEGO_WORLD_RPC_ADDR", "127.0.0.1:19485")}}
	if raw := os.Getenv("MAPLEGO_WORLDS_JSON"); raw != "" {
		if err = json.Unmarshal([]byte(raw), &c.Worlds); err != nil {
			return c, fmt.Errorf("MAPLEGO_WORLDS_JSON: %w", err)
		}
	}
	defaultHealth := 19080
	if kind == "world" {
		defaultHealth = 19081 + c.WorldID*2
	}
	if kind == "cashshop" {
		defaultHealth = 19082
	}
	if kind == "admin" {
		defaultHealth = 19079
	}
	c.HealthAddr = value("MAPLEGO_HEALTH_ADDR", net.JoinHostPort(c.Bind, strconv.Itoa(defaultHealth)))
	if err = c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

func loopback(host string) bool { ip := net.ParseIP(host); return ip != nil && ip.IsLoopback() }
func loopbackEndpoint(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || !loopback(host) {
		return false
	}
	p, e := strconv.Atoi(port)
	return e == nil && p > 0 && p < 65536
}

func (c Config) Validate() error {
	if !loopback(c.Bind) || !loopbackEndpoint(c.HealthAddr) || !loopbackEndpoint(c.RedisAddr) {
		return fmt.Errorf("all runtime endpoints must bind to loopback")
	}
	if c.LoginPort < 1 || c.LoginPort > 65535 || c.CashshopPort < 1 || c.CashshopPort > 65535 {
		return fmt.Errorf("invalid service port")
	}
	if c.DatabaseURL == "" {
		return fmt.Errorf("MAPLEGO_DATABASE_URL is required")
	}
	u, err := url.Parse(c.DatabaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || !loopback(u.Hostname()) || !strings.HasPrefix(strings.TrimPrefix(u.Path, "/"), "maple_go_") {
		return fmt.Errorf("database must be an isolated loopback maple_go_* PostgreSQL database")
	}
	if len(c.RPCToken) < 32 {
		return fmt.Errorf("MAPLEGO_RPC_TOKEN must contain at least 32 characters")
	}
	if len(c.Worlds) == 0 {
		return fmt.Errorf("at least one world is required")
	}
	seen := map[int]bool{}
	for _, w := range c.Worlds {
		if w.ID < 0 || w.ID > 254 || seen[w.ID] || !loopback(w.Host) || !loopbackEndpoint(w.RPCAddr) || w.ChannelCount < 1 || w.ChannelCount > 20 || w.ChannelBasePort < 1 || w.ChannelBasePort+w.ChannelCount > 65536 {
			return fmt.Errorf("invalid or duplicate world configuration")
		}
		seen[w.ID] = true
	}
	if c.Kind == "world" && !seen[c.WorldID] {
		return fmt.Errorf("world %d is absent from directory", c.WorldID)
	}
	return nil
}

func (c Config) World(id int) (WorldConfig, bool) {
	for _, w := range c.Worlds {
		if w.ID == id {
			return w, true
		}
	}
	return WorldConfig{}, false
}
