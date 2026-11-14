package rpc

import (
	"context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"testing"
)

func TestServiceRPCRequiresExactSingleCredential(t *testing.T) {
	token := "01234567890123456789012345678901"
	for _, tc := range []struct {
		name    string
		values  []string
		allowed bool
	}{
		{"missing", nil, false}, {"wrong", []string{"Bearer wrong"}, false}, {"duplicate", []string{"Bearer " + token, "Bearer " + token}, false}, {"valid", []string{"Bearer " + token}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.values != nil {
				ctx = metadata.NewIncomingContext(ctx, metadata.MD{"authorization": tc.values})
			}
			called := false
			_, err := AuthInterceptor(token)(ctx, nil, &grpc.UnaryServerInfo{}, func(context.Context, any) (any, error) { called = true; return "ok", nil })
			if called != tc.allowed {
				t.Fatalf("called=%v expected=%v", called, tc.allowed)
			}
			if !tc.allowed && status.Code(err) != codes.Unauthenticated {
				t.Fatalf("expected Unauthenticated, got %v", err)
			}
		})
	}
}
