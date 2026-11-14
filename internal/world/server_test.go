package world

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	maplev1 "maple-go-server/api/maplev1"
	"maple-go-server/internal/platform"
	"testing"
)

func TestCreateCharacterRejectsCrossWorldAndUnready(t *testing.T) {
	s := &Server{cfg: platform.Config{WorldID: 1}}
	if _, err := s.CreateCharacter(context.Background(), &maplev1.CreateCharacterRequest{WorldId: 0}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-world err=%v", err)
	}
	if _, err := s.CreateCharacter(context.Background(), &maplev1.CreateCharacterRequest{WorldId: 1}); status.Code(err) != codes.Unavailable {
		t.Fatalf("unready err=%v", err)
	}
	s.ready.Store(true)
	if _, err := s.CreateCharacter(context.Background(), &maplev1.CreateCharacterRequest{WorldId: 1}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing operation ID err=%v", err)
	}
	if _, err := s.CreateCharacter(context.Background(), &maplev1.CreateCharacterRequest{WorldId: 1, OperationId: "new-character-operation"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing login session err=%v", err)
	}
}
