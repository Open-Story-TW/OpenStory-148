package cashshop

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

type cashAdminRequest struct {
	Action      string    `json:"action"`
	OperationID string    `json:"operation_id"`
	AccountName string    `json:"account_name"`
	Code        string    `json:"code"`
	Kind        string    `json:"kind"`
	Value       int64     `json:"value"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func adminJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// adminCash is an operator-only control on the existing loopback health
// listener. Issuing a coupon never grants value; redemption must still occur
// through the normal authenticated cashshop transaction.
func (s *server) adminCash(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.cfg.RPCToken)) != 1 {
		adminJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		adminJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var req cashAdminRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF || req.Action != "issue_coupon" {
		adminJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	repo, ok := s.repo.(domain.CashCouponOperatorRepository)
	if !ok {
		adminJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "operator_repository_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	result, err := repo.IssueCashCoupon(ctx, domain.CashCouponIssueRequest{OperationID: req.OperationID, Actor: "local-operator", AccountName: req.AccountName, Code: req.Code, Kind: req.Kind, Value: req.Value, ExpiresAt: req.ExpiresAt})
	if err != nil {
		status, message := http.StatusInternalServerError, "coupon_issue_failed"
		switch {
		case errors.Is(err, domain.ErrInvalidInput):
			status, message = http.StatusBadRequest, "invalid_request"
		case errors.Is(err, domain.ErrNotFound):
			status, message = http.StatusNotFound, "account_not_found"
		case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrForbidden):
			status, message = http.StatusConflict, "operation_conflict"
		}
		if status == http.StatusInternalServerError {
			slog.Error("operator_coupon_issue_failed", "operation", req.OperationID)
		}
		adminJSON(w, status, map[string]string{"error": message})
		return
	}
	adminJSON(w, http.StatusOK, result)
}
