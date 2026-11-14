package world

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"maple-go-server/internal/domain"
)

// Registered only on the configured loopback health listener. The operator
// credential grants an expiring exact QA identity; it never executes commands.
func (s *Server) gmAuthorizations(w http.ResponseWriter, r *http.Request) {
	if len(s.cfg.RPCToken) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.cfg.RPCToken)) != 1 {
		debugJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		debugJSON(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var req domain.QAGMGrantRequest
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF {
		debugJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	repo, ok := s.repo.(domain.GMRepository)
	if !ok {
		debugJSON(w, 503, map[string]string{"error": "gm_unavailable"})
		return
	}
	req.Actor = "local-operator"
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	result, err := repo.AuthorizeQAGM(ctx, req)
	if err != nil {
		status, message := 500, "authorization_failed"
		switch {
		case errors.Is(err, domain.ErrInvalidInput):
			status, message = 400, "invalid_request"
		case errors.Is(err, domain.ErrForbidden):
			status, message = 403, "forbidden"
		case errors.Is(err, domain.ErrNotFound):
			status, message = 404, "not_found"
		case errors.Is(err, domain.ErrAlreadyExists):
			status, message = 409, "account_must_be_offline"
		}
		debugJSON(w, status, map[string]string{"error": message})
		return
	}
	debugJSON(w, 200, result)
}
