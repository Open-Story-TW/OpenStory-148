package cashshop

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
)

type couponOperatorStub struct {
	domain.CashRepository
	requests []domain.CashCouponIssueRequest
}

func (s *couponOperatorStub) IssueCashCoupon(_ context.Context, req domain.CashCouponIssueRequest) (domain.CashCouponIssueResult, error) {
	s.requests = append(s.requests, req)
	return domain.CashCouponIssueResult{OperationID: req.OperationID, Actor: req.Actor, AccountName: req.AccountName, AccountID: 1, CodeHash: "audit-hash", Kind: req.Kind, Value: req.Value, ExpiresAt: req.ExpiresAt}, nil
}

func TestCouponOperatorRequiresAuthenticationAndFixesActor(t *testing.T) {
	repo := &couponOperatorStub{}
	s := &server{cfg: platform.Config{RPCToken: "operator-secret"}, repo: repo}
	body := `{"action":"issue_coupon","operation_id":"qa-coupon-operation-0001","account_name":"goqa01","code":"PRIVATE-COUPON-1234","kind":"credit","value":1000,"expires_at":"2026-11-02T00:00:00Z"}`
	call := func(method, token, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "/admin/cash", strings.NewReader(body))
		request.Header.Set("Authorization", token)
		response := httptest.NewRecorder()
		s.adminCash(response, request)
		return response
	}
	if r := call("POST", "", body); r.Code != 401 || len(repo.requests) != 0 {
		t.Fatal("unauthenticated coupon issue reached the repository")
	}
	if r := call("GET", "Bearer operator-secret", body); r.Code != 405 {
		t.Fatal("operator accepts unsupported method")
	}
	if r := call("POST", "Bearer operator-secret", strings.TrimSuffix(body, "}")+`,"actor":"impersonated"}`); r.Code != 400 {
		t.Fatal("caller selected an audit principal")
	}
	if r := call("POST", "Bearer operator-secret", body+` {}`); r.Code != 400 {
		t.Fatal("accepted trailing request object")
	}
	r := call("POST", "Bearer operator-secret", body)
	if r.Code != 200 || len(repo.requests) != 1 || repo.requests[0].Actor != "local-operator" {
		t.Fatalf("operator issue failed: %d %s", r.Code, r.Body.String())
	}
	if strings.Contains(r.Body.String(), "PRIVATE-COUPON") || !strings.Contains(r.Body.String(), "audit-hash") {
		t.Fatal("operator response disclosed the redeemable credential")
	}
}
