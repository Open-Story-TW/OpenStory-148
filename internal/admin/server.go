// Package admin is the web admin: server settings, service and player status,
// GM grants and the list of administrators. It is the one service meant to be
// reachable from outside the machine, so every route except sign-in requires a
// session opened with an emailed one-time code.
package admin

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
)

//go:embed web
var webFiles embed.FS

type Server struct {
	cfg       platform.Config
	db        *sql.DB
	repo      domain.Repository
	mail      mailer
	bootstrap map[string]bool
	limiter   *limiter
	client    *http.Client
	iconsOnce sync.Once
	iconStore *iconStore
}

type database interface{ Database() *sql.DB }

func env(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

// Run serves the admin until ctx ends. MAPLEGO_ADMIN_ADDR may name any
// interface; the game services themselves stay on loopback.
func Run(ctx context.Context, cfg platform.Config, repo domain.Repository, health *platform.Health) error {
	store, ok := repo.(database)
	if !ok {
		return errors.New("admin requires the PostgreSQL store")
	}
	s := &Server{cfg: cfg, db: store.Database(), repo: repo, bootstrap: map[string]bool{}, limiter: newLimiter(),
		client: &http.Client{Timeout: 2 * time.Second}}
	for _, email := range strings.Split(os.Getenv("MAPLEGO_ADMIN_EMAILS"), ",") {
		if email = normalizeEmail(email); email != "" {
			s.bootstrap[email] = true
		}
	}
	s.mail = mailer{token: os.Getenv("MAPLEGO_POSTMARK_TOKEN"), from: strings.TrimSpace(os.Getenv("MAPLEGO_ADMIN_MAIL_FROM")),
		client: &http.Client{Timeout: 10 * time.Second}}
	addr := env("MAPLEGO_ADMIN_ADDR", "0.0.0.0:19090")
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: s.routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	cert, key := os.Getenv("MAPLEGO_ADMIN_TLS_CERT"), os.Getenv("MAPLEGO_ADMIN_TLS_KEY")
	failed := make(chan error, 1)
	go func() {
		var err error
		if cert != "" && key != "" {
			err = server.ServeTLS(listener, cert, key)
		} else {
			err = server.Serve(listener)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			failed <- err
		}
	}()
	go s.sweep(ctx)
	go s.collect(ctx)
	health.Detail("admin_addr", addr)
	health.Detail("mail_configured", s.mail.configured())
	health.Ready(true)
	slog.Info("admin_listening", "addr", addr, "tls", cert != "" && key != "", "mail_configured", s.mail.configured(), "bootstrap_admins", len(s.bootstrap))
	select {
	case err = <-failed:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	return nil
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(webFiles, "web")
	files := http.FileServer(http.FS(static))
	mux.Handle("GET /", files)
	mux.HandleFunc("POST /api/login/request", s.loginRequest)
	mux.HandleFunc("POST /api/login/verify", s.loginVerify)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/me", s.authed(s.me))
	mux.HandleFunc("GET /api/settings", s.authed(s.getSettings))
	mux.HandleFunc("POST /api/settings", s.authed(s.postSettings))
	mux.HandleFunc("GET /api/status", s.authed(s.status))
	mux.HandleFunc("GET /api/players", s.authed(s.players))
	mux.HandleFunc("GET /api/live", s.authed(s.live))
	mux.HandleFunc("GET /api/character", s.authed(s.character))
	mux.HandleFunc("POST /api/character/inventory", s.authed(s.characterInventory))
	mux.HandleFunc("POST /api/character/wallet", s.authed(s.characterWallet))
	mux.HandleFunc("GET /api/items/search", s.authed(s.itemSearch))
	mux.HandleFunc("GET /api/items/icon", s.authed(s.itemIcon))
	mux.HandleFunc("GET /api/events", s.authed(s.events))
	mux.HandleFunc("GET /api/notices", s.authed(s.noticeList))
	mux.HandleFunc("POST /api/notices", s.authed(s.noticeSend))
	mux.HandleFunc("GET /api/gm", s.authed(s.gmList))
	mux.HandleFunc("POST /api/gm", s.authed(s.gmChange))
	mux.HandleFunc("GET /api/admins", s.authed(s.adminList))
	mux.HandleFunc("POST /api/admins", s.authed(s.adminChange))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
			// A cross-site form cannot set this header or this content type.
			if r.Method == http.MethodPost && (r.Header.Get("X-Maple-Admin") != "1" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")) {
				writeError(w, http.StatusForbidden, "請從後台頁面操作")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(v) != nil || decoder.Decode(new(any)) != io.EOF {
		writeError(w, http.StatusBadRequest, "請求格式錯誤")
		return false
	}
	return true
}

// clientIP trusts a forwarded address only from a proxy on this machine,
// such as a tunnel that terminates TLS.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		for _, name := range []string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For"} {
			if v := strings.TrimSpace(strings.Split(r.Header.Get(name), ",")[0]); net.ParseIP(v) != nil {
				return v
			}
		}
	}
	return host
}

func secureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) sweep(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			clean, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, _ = s.db.ExecContext(clean, `DELETE FROM ops_admin_sessions WHERE expires_at<now()`)
			_, _ = s.db.ExecContext(clean, `DELETE FROM ops_admin_login_codes WHERE expires_at<now()`)
			_, _ = s.db.ExecContext(clean, `DELETE FROM ops_admin_events WHERE at<$1`, time.Now().Add(-eventRetention))
			cancel()
			s.limiter.sweep()
		}
	}
}

func (s *Server) serviceURL(kind string, world int) string {
	port := 19080
	switch kind {
	case "world":
		port = 19081 + world*2
	case "cashshop":
		port = 19082
	}
	return fmt.Sprintf("http://127.0.0.1:%d/health/ready", port)
}
