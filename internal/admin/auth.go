package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie = "maplego_admin"
	sessionLife   = 7 * 24 * time.Hour
	codeLife      = 10 * time.Minute
	codeResend    = time.Minute
	codeAttempts  = 5
)

func normalizeEmail(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if len(raw) < 3 || len(raw) > 254 {
		return ""
	}
	parsed, err := mail.ParseAddress(raw)
	if err != nil || parsed.Address != raw || !strings.Contains(raw, "@") {
		return ""
	}
	return raw
}

func digest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// limiter bounds sign-in traffic per client address.
type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newLimiter() *limiter { return &limiter{hits: map[string][]time.Time{}} }

func (l *limiter) allow(key string, limit int, window time.Duration) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= limit {
		l.hits[key] = recent
		return false
	}
	l.hits[key] = append(recent, now)
	return true
}

func (l *limiter) sweep() {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, hits := range l.hits {
		if len(hits) == 0 || now.Sub(hits[len(hits)-1]) > time.Hour {
			delete(l.hits, key)
		}
	}
}

type mailer struct {
	token, from string
	client      *http.Client
}

func (m mailer) configured() bool { return m.token != "" && m.from != "" }

func (m mailer) send(ctx context.Context, to, subject, body string) error {
	payload, _ := json.Marshal(map[string]string{"From": m.from, "To": to, "Subject": subject, "TextBody": body, "MessageStream": "outbound"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.postmarkapp.com/email", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Postmark-Server-Token", m.token)
	res, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		var reply struct {
			ErrorCode int
			Message   string
		}
		_ = json.NewDecoder(res.Body).Decode(&reply)
		return fmt.Errorf("postmark status %d code %d: %s", res.StatusCode, reply.ErrorCode, reply.Message)
	}
	return nil
}

func (s *Server) isAdmin(ctx context.Context, email string) (bool, error) {
	if email == "" {
		return false, nil
	}
	if s.bootstrap[email] {
		return true, nil
	}
	var found bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ops_admin_users WHERE email=$1)`, email).Scan(&found)
	return found, err
}

// loginRequest answers the same way whether or not the address is an
// administrator, so the form does not reveal who is.
func (s *Server) loginRequest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !s.limiter.allow("request:"+clientIP(r), 8, 10*time.Minute) {
		writeError(w, http.StatusTooManyRequests, "嘗試太多次，請 10 分鐘後再試")
		return
	}
	email := normalizeEmail(req.Email)
	if email == "" {
		writeError(w, http.StatusBadRequest, "請輸入正確的 email")
		return
	}
	accepted := map[string]any{"ok": true}
	admin, err := s.isAdmin(r.Context(), email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "伺服器錯誤")
		return
	}
	if !admin {
		slog.Warn("admin_login_unknown_email", "ip", clientIP(r))
		writeJSON(w, http.StatusOK, accepted)
		return
	}
	var sent time.Time
	err = s.db.QueryRowContext(r.Context(), `SELECT sent_at FROM ops_admin_login_codes WHERE email=$1`, email).Scan(&sent)
	if err == nil && time.Since(sent) < codeResend {
		writeJSON(w, http.StatusOK, accepted)
		return
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "伺服器錯誤")
		return
	}
	code := fmt.Sprintf("%06d", n.Int64())
	if _, err = s.db.ExecContext(r.Context(), `INSERT INTO ops_admin_login_codes(email,code_hash,expires_at) VALUES($1,$2,$3) ON CONFLICT(email) DO UPDATE SET code_hash=excluded.code_hash,attempts=0,sent_at=clock_timestamp(),expires_at=excluded.expires_at`, email, digest(email, code), time.Now().Add(codeLife)); err != nil {
		writeError(w, http.StatusInternalServerError, "伺服器錯誤")
		return
	}
	if !s.mail.configured() {
		// Without a mail provider the code is only readable on the machine.
		slog.Warn("admin_login_code_not_mailed", "email", email, "code", code, "reason", "MAPLEGO_POSTMARK_TOKEN or MAPLEGO_ADMIN_MAIL_FROM is not set")
		writeJSON(w, http.StatusOK, accepted)
		return
	}
	body := "你的 Open Maple 後台登入認證碼：\n\n    " + code + "\n\n10 分鐘內有效。如果不是你本人操作，請忽略這封信。\n"
	if err = s.mail.send(r.Context(), email, "Open Maple 後台認證碼 "+code, body); err != nil {
		slog.Error("admin_login_mail_failed", "error", err)
		writeError(w, http.StatusBadGateway, "認證信寄送失敗，請查看後台服務的紀錄")
		return
	}
	writeJSON(w, http.StatusOK, accepted)
}

func (s *Server) loginVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !s.limiter.allow("verify:"+clientIP(r), 20, 10*time.Minute) {
		writeError(w, http.StatusTooManyRequests, "嘗試太多次，請 10 分鐘後再試")
		return
	}
	email, code := normalizeEmail(req.Email), strings.TrimSpace(req.Code)
	const wrong = "認證碼錯誤或已過期"
	if email == "" || len(code) != 6 {
		writeError(w, http.StatusUnauthorized, wrong)
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "伺服器錯誤")
		return
	}
	defer tx.Rollback()
	var hash string
	var attempts int
	var expires time.Time
	err = tx.QueryRowContext(r.Context(), `SELECT code_hash,attempts,expires_at FROM ops_admin_login_codes WHERE email=$1 FOR UPDATE`, email).Scan(&hash, &attempts, &expires)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (attempts >= codeAttempts || time.Now().After(expires)) {
		writeError(w, http.StatusUnauthorized, wrong)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "伺服器錯誤")
		return
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(digest(email, code))) != 1 {
		if _, err = tx.ExecContext(r.Context(), `UPDATE ops_admin_login_codes SET attempts=attempts+1 WHERE email=$1`, email); err == nil {
			err = tx.Commit()
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "伺服器錯誤")
			return
		}
		writeError(w, http.StatusUnauthorized, wrong)
		return
	}
	// The address may have lost its rights after the code was sent.
	admin, err := s.isAdmin(r.Context(), email)
	if err != nil || !admin {
		writeError(w, http.StatusUnauthorized, wrong)
		return
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, "伺服器錯誤")
		return
	}
	token := hex.EncodeToString(raw)
	expiry := time.Now().Add(sessionLife)
	if _, err = tx.ExecContext(r.Context(), `DELETE FROM ops_admin_login_codes WHERE email=$1`, email); err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO ops_admin_sessions(token_hash,email,expires_at) VALUES($1,$2,$3)`, digest(token), email, expiry)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "伺服器錯誤")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Expires: expiry, HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteStrictMode})
	slog.Info("admin_login", "email", email, "ip", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "email": email})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		_, _ = s.db.ExecContext(r.Context(), `DELETE FROM ops_admin_sessions WHERE token_hash=$1`, digest(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type adminKey struct{}

// authed admits a request whose session is unexpired and whose address is
// still an administrator.
func (s *Server) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil || len(cookie.Value) != 64 {
			writeError(w, http.StatusUnauthorized, "請先登入")
			return
		}
		var email string
		err = s.db.QueryRowContext(r.Context(), `SELECT email FROM ops_admin_sessions WHERE token_hash=$1 AND expires_at>now()`, digest(cookie.Value)).Scan(&email)
		if err == nil {
			var admin bool
			if admin, err = s.isAdmin(r.Context(), email); err == nil && !admin {
				err = sql.ErrNoRows
			}
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusUnauthorized, "請先登入")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "伺服器錯誤")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), adminKey{}, email)))
	}
}

func actor(r *http.Request) string {
	email, _ := r.Context().Value(adminKey{}).(string)
	return email
}
