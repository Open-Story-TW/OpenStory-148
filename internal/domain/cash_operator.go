package domain

import (
	"context"
	"time"
)

type CashCouponIssueRequest struct {
	OperationID string    `json:"operation_id"`
	Actor       string    `json:"actor"`
	AccountName string    `json:"account_name"`
	Code        string    `json:"code"`
	Kind        string    `json:"kind"`
	Value       int64     `json:"value"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type CashCouponIssueResult struct {
	OperationID string    `json:"operation_id"`
	Actor       string    `json:"actor"`
	AccountID   int64     `json:"account_id"`
	AccountName string    `json:"account_name"`
	CodeHash    string    `json:"code_hash"`
	Kind        string    `json:"kind"`
	Value       int64     `json:"value"`
	ExpiresAt   time.Time `json:"expires_at"`
	Replayed    bool      `json:"replayed"`
}

// Only a separately authenticated operator facade may expose this capability.
// The facade supplies Actor from its authenticated principal, never request JSON.
type CashCouponOperatorRepository interface {
	IssueCashCoupon(context.Context, CashCouponIssueRequest) (CashCouponIssueResult, error)
}
