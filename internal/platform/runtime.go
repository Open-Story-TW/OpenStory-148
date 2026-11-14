package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

type Health struct {
	cfg     Config
	ready   atomic.Bool
	mu      sync.RWMutex
	details map[string]any
	ping    func(context.Context) error
	mux     *http.ServeMux
	dynamic []dynamicDetail
}

type dynamicDetail struct {
	name  string
	value func() any
}

func NewHealth(cfg Config, ping func(context.Context) error) *Health {
	return &Health{cfg: cfg, ping: ping, details: map[string]any{}, mux: http.NewServeMux()}
}

// Handle attaches service-specific endpoints. Callers remain responsible for
// authentication; the listener itself is restricted to the configured loopback.
func (h *Health) Handle(pattern string, handler http.Handler) { h.mux.Handle(pattern, handler) }
func (h *Health) Ready(v bool)                                { h.ready.Store(v) }
func (h *Health) Detail(name string, value any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.details[name] = value
}

// DetailFunc reports a value read anew for every readiness response.
func (h *Health) DetailFunc(name string, value func() any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dynamic = append(h.dynamic, dynamicDetail{name, value})
}
func (h *Health) Serve(ctx context.Context) error {
	listener, err := net.Listen("tcp", h.cfg.HealthAddr)
	if err != nil {
		return err
	}
	mux := h.mux
	mux.HandleFunc("/health/live", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "alive", "service": h.cfg.Kind, "instance": h.cfg.InstanceID, "pid": os.Getpid()})
	})
	mux.HandleFunc("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		state := "ready"
		status := 200
		if !h.ready.Load() {
			state = "starting"
			status = 503
		} else {
			pctx, cancel := context.WithTimeout(r.Context(), time.Second)
			defer cancel()
			if err := h.ping(pctx); err != nil {
				state = "database_unavailable"
				status = 503
			}
		}
		h.mu.RLock()
		defer h.mu.RUnlock()
		details := make(map[string]any, len(h.details)+len(h.dynamic))
		for name, value := range h.details {
			details[name] = value
		}
		for _, d := range h.dynamic {
			details[d.name] = d.value()
		}
		writeJSON(w, status, map[string]any{"status": state, "service": h.cfg.Kind, "instance": h.cfg.InstanceID, "pid": os.Getpid(), "details": details})
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		h.Ready(false)
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("health listener failed", "error", err)
		}
	}()
	return nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// TCPGroup owns only its own listeners/connections. Cancelling this context
// cannot stop another service or the untouched Java baseline.
type TCPGroup struct {
	mu        sync.Mutex
	listeners []net.Listener
	conns     map[net.Conn]bool
	wg        sync.WaitGroup
	closing   bool
}

func NewTCPGroup() *TCPGroup { return &TCPGroup{conns: map[net.Conn]bool{}} }
func (g *TCPGroup) Listen(ctx context.Context, address string, handler func(context.Context, net.Conn)) error {
	l, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	g.mu.Lock()
	g.listeners = append(g.listeners, l)
	g.mu.Unlock()
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		for {
			conn, err := l.Accept()
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("listener stopped", "address", address, "error", err)
				}
				return
			}
			g.mu.Lock()
			if g.closing {
				g.mu.Unlock()
				conn.Close()
				return
			}
			if len(g.conns) >= 6000 {
				g.mu.Unlock()
				conn.Close()
				continue
			}
			g.conns[conn] = true
			g.wg.Add(1)
			g.mu.Unlock()
			go func() {
				defer g.wg.Done()
				defer conn.Close()
				defer func() {
					g.mu.Lock()
					delete(g.conns, conn)
					g.mu.Unlock()
					if v := recover(); v != nil {
						slog.Error("connection handler panic", "panic", fmt.Sprint(v))
					}
				}()
				handler(ctx, conn)
			}()
		}
	}()
	return nil
}
func (g *TCPGroup) Close() {
	g.mu.Lock()
	g.closing = true
	for _, l := range g.listeners {
		_ = l.Close()
	}
	for c := range g.conns {
		_ = c.Close()
	}
	g.mu.Unlock()
	g.wg.Wait()
}
