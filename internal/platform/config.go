package platform

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"maple-go-server/internal/domain"
)

type WorldConfig struct {
	ID              int    `json:"id"`
	Name            string `json:"name"`
	Host            string `json:"host"`
	ChannelBasePort int    `json:"channel_base_port"`
	ChannelCount    int    `json:"channel_count"`
	RPCAddr         string `json:"rpc_addr"`
	// PublicChannelPorts, when set, are the ports clients are told to use
	// for channels 1..ChannelCount. A proxy in front of the server that
	// assigns its own ports needs them; the world still listens on
	// ChannelBasePort and up. Public deployment only.
	PublicChannelPorts []int `json:"public_channel_ports,omitempty"`
}

// ChannelPort is the port the world listens on for channel.
func (w WorldConfig) ChannelPort(channel int) int { return w.ChannelBasePort + channel - 1 }

// AdvertisedChannelPort is the port a client is told to connect to for channel.
func (w WorldConfig) AdvertisedChannelPort(channel int) int {
	if channel >= 1 && channel <= len(w.PublicChannelPorts) {
		return w.PublicChannelPorts[channel-1]
	}
	return w.ChannelPort(channel)
}

type Config struct {
	Kind          string
	InstanceID    string
	DatabaseURL   string
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	Bind          string
	// Public is the deployment mode set by MAPLEGO_DEPLOY=public: listeners,
	// the database and the cache may be off loopback, and clients are sent
	// to PublicHost. Without it every endpoint must be loopback.
	Public bool
	// PublicHost is the IPv4 address or DNS name players connect to. A name
	// is looked up when a client is told where to reconnect.
	PublicHost string
	// PublicCashshopPort is the cash shop port clients are told to use when
	// a proxy assigns its own; zero means CashshopPort.
	PublicCashshopPort int
	// TailnetBind, when set, is this machine's Tailscale address. The login,
	// channel and cash shop listeners open on it as well as on Bind, so the
	// operator's other tailnet devices can play. Nothing else listens there.
	TailnetBind         string
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

// Load accepts only isolated loopback development endpoints, plus the game
// listeners on the operator's own tailnet when MAPLEGO_TAILNET_BIND is set.
// MAPLEGO_DEPLOY=public lifts the loopback rule for a server players reach
// over the internet. That mode does not change who may enter the game: the
// handoff that identifies a client by address alone stays limited to
// loopback and the tailnet (domain.TrustedClientIP), so production network
// exposure still requires a client handoff credential with a reviewed
// security contract.
func Load(kind string) (Config, error) {
	c := Config{Kind: kind, InstanceID: value("MAPLEGO_INSTANCE_ID", NewID()),
		DatabaseURL: os.Getenv("MAPLEGO_DATABASE_URL"), RedisAddr: value("MAPLEGO_REDIS_ADDR", "127.0.0.1:16379"),
		RedisPassword: os.Getenv("MAPLEGO_REDIS_PASSWORD"), TailnetBind: os.Getenv("MAPLEGO_TAILNET_BIND"),
		RPCToken: os.Getenv("MAPLEGO_RPC_TOKEN"), StopFile: os.Getenv("MAPLEGO_STOP_FILE"), ScriptsDir: os.Getenv("MAPLEGO_SCRIPTS_DIR"),
		AllowLegacyLoopback: value("MAPLEGO_LEGACY_LOOPBACK", "true") == "true"}
	deploy := value("MAPLEGO_DEPLOY", "local")
	if deploy != "local" && deploy != "public" {
		return c, fmt.Errorf("MAPLEGO_DEPLOY must be local or public")
	}
	c.Public = deploy == "public"
	c.PublicHost = os.Getenv("MAPLEGO_PUBLIC_HOST")
	c.Bind = value("MAPLEGO_BIND", "127.0.0.1")
	if c.Public {
		c.Bind = value("MAPLEGO_BIND", "0.0.0.0")
	}
	var err error
	for _, entry := range []struct {
		name     string
		dst      *int
		fallback int
	}{
		{"MAPLEGO_LOGIN_PORT", &c.LoginPort, 18484}, {"MAPLEGO_CASHSHOP_PORT", &c.CashshopPort, 18600},
		{"MAPLEGO_WORLD_ID", &c.WorldID, 0}, {"MAPLEGO_REDIS_DB", &c.RedisDB, 0},
		{"MAPLEGO_PUBLIC_CASHSHOP_PORT", &c.PublicCashshopPort, 0},
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
	c.Worlds = []WorldConfig{{ID: 0, Name: "Go 雪吉拉", Host: c.CashshopHost(), ChannelBasePort: base, ChannelCount: count, RPCAddr: value("MAPLEGO_WORLD_RPC_ADDR", "127.0.0.1:19485")}}
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
	// Health also serves operator endpoints, so it stays on loopback unless
	// MAPLEGO_HEALTH_ADDR says otherwise.
	healthHost := c.Bind
	if c.Public {
		healthHost = "127.0.0.1"
	}
	c.HealthAddr = value("MAPLEGO_HEALTH_ADDR", net.JoinHostPort(healthHost, strconv.Itoa(defaultHealth)))
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

// endpoint reports whether addr is host:port with a usable port.
func endpoint(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	p, e := strconv.Atoi(port)
	return e == nil && validPort(p)
}

var dnsName = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$`)

// publicHost reports whether host can be given to players: an IPv4 address
// that is neither loopback nor unspecified, or a DNS name.
func publicHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.To4() != nil && !ip.IsLoopback() && !ip.IsUnspecified()
	}
	return len(host) <= 253 && dnsName.MatchString(host)
}

func validPort(p int) bool { return p > 0 && p < 65536 }

func (c Config) Validate() error {
	if c.Public {
		if net.ParseIP(c.Bind) == nil || !endpoint(c.HealthAddr) || !endpoint(c.RedisAddr) {
			return fmt.Errorf("MAPLEGO_BIND must be an IP address, and the health and Redis addresses host:port")
		}
		if !publicHost(c.PublicHost) {
			return fmt.Errorf("MAPLEGO_PUBLIC_HOST must be the public IPv4 address or DNS name players connect to")
		}
		if c.PublicCashshopPort != 0 && !validPort(c.PublicCashshopPort) {
			return fmt.Errorf("invalid MAPLEGO_PUBLIC_CASHSHOP_PORT")
		}
	} else {
		if !loopback(c.Bind) || !loopbackEndpoint(c.HealthAddr) || !loopbackEndpoint(c.RedisAddr) {
			return fmt.Errorf("all runtime endpoints must bind to loopback")
		}
		if c.PublicHost != "" || c.PublicCashshopPort != 0 {
			return fmt.Errorf("MAPLEGO_PUBLIC_HOST and MAPLEGO_PUBLIC_CASHSHOP_PORT need MAPLEGO_DEPLOY=public")
		}
	}
	if c.TailnetBind != "" && !domain.TailnetIP(c.TailnetBind) {
		return fmt.Errorf("MAPLEGO_TAILNET_BIND must be a Tailscale address (100.64.0.0/10)")
	}
	if c.LoginPort < 1 || c.LoginPort > 65535 || c.CashshopPort < 1 || c.CashshopPort > 65535 {
		return fmt.Errorf("invalid service port")
	}
	if c.DatabaseURL == "" {
		return fmt.Errorf("MAPLEGO_DATABASE_URL is required")
	}
	u, err := url.Parse(c.DatabaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" {
		return fmt.Errorf("MAPLEGO_DATABASE_URL must be a PostgreSQL URL")
	}
	if !c.Public && (!loopback(u.Hostname()) || !strings.HasPrefix(strings.TrimPrefix(u.Path, "/"), "maple_go_")) {
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
		hostOK, rpcOK := loopback(w.Host) && len(w.PublicChannelPorts) == 0, loopbackEndpoint(w.RPCAddr)
		if c.Public {
			hostOK, rpcOK = publicHost(w.Host) && (len(w.PublicChannelPorts) == 0 || len(w.PublicChannelPorts) == w.ChannelCount), endpoint(w.RPCAddr)
			for _, p := range w.PublicChannelPorts {
				hostOK = hostOK && validPort(p)
			}
		}
		if w.ID < 0 || w.ID > 254 || seen[w.ID] || !hostOK || !rpcOK || w.ChannelCount < 1 || w.ChannelCount > 20 || w.ChannelBasePort < 1 || w.ChannelBasePort+w.ChannelCount > 65536 {
			return fmt.Errorf("invalid or duplicate world configuration")
		}
		seen[w.ID] = true
	}
	if c.Kind == "world" && !seen[c.WorldID] {
		return fmt.Errorf("world %d is absent from directory", c.WorldID)
	}
	return nil
}

// ListenHosts are the addresses the game listeners open on.
func (c Config) ListenHosts() []string {
	if c.TailnetBind == "" {
		return []string{c.Bind}
	}
	return []string{c.Bind, c.TailnetBind}
}

// CashshopHost is the address a client is told to use for the cash shop, and
// the default host of a world.
func (c Config) CashshopHost() string {
	if c.Public {
		return c.PublicHost
	}
	return c.Bind
}

// AdvertisedCashshopPort is the cash shop port a client is told to connect to.
func (c Config) AdvertisedCashshopPort() int {
	if c.PublicCashshopPort != 0 {
		return c.PublicCashshopPort
	}
	return c.CashshopPort
}

func (c Config) World(id int) (WorldConfig, bool) {
	for _, w := range c.Worlds {
		if w.ID == id {
			return w, true
		}
	}
	return WorldConfig{}, false
}
