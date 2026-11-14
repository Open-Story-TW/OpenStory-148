package world

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"maple-go-server/internal/domain"
)

// qaFamilyAdmin switches an offline QA-granted character to another job
// family with its starter look (Mercedes/Demon/Phantom are not creatable by
// the v145 client). Loopback health listener and operator token only.
func (s *Server) qaFamilyAdmin(w http.ResponseWriter, r *http.Request) {
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
	var req domain.QAFamilyRequest
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF {
		debugJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	repo, ok := s.repo.(domain.QAFamilyRepository)
	if !ok {
		debugJSON(w, 503, map[string]string{"error": "qa_family_unavailable"})
		return
	}
	req.Actor = "local-operator"
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	result, err := repo.ApplyQAFamily(ctx, req)
	if err != nil {
		status, message := 500, "qa_family_failed"
		slog.Warn("qa_family_failed", "character", req.CharacterID, "job", req.Job, "error", err)
		switch {
		case errors.Is(err, domain.ErrInvalidInput):
			status, message = 400, "invalid_request"
		case errors.Is(err, domain.ErrForbidden):
			status, message = 403, "no_active_qa_grant_or_operation_mismatch"
		case errors.Is(err, domain.ErrNotFound):
			status, message = 404, "not_found"
		case errors.Is(err, domain.ErrAlreadyExists):
			status, message = 409, "account_must_be_offline"
		case errors.Is(err, domain.ErrInsufficient):
			status, message = 409, "equip_inventory_full"
		}
		debugJSON(w, status, map[string]string{"error": message})
		return
	}
	debugJSON(w, 200, result)
}
