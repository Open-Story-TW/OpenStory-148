package persistence

import (
	"context"
	"errors"
	"maple-go-server/internal/domain"
	"testing"
)

func TestStarterCreationValidation(t *testing.T) {
	male := domain.CreateCharacterRequest{AccountID: 1, WorldID: 0, Name: "GoPilot", Gender: 0}
	if err := normalizeCreate(&male); err != nil {
		t.Fatal(err)
	}
	if male.Face != 20100 || male.Hair != 30000 || male.Top != 1040010 {
		t.Fatal("incorrect stock defaults")
	}
	female := domain.CreateCharacterRequest{AccountID: 1, WorldID: 0, Name: "GoPilotF", Gender: 1}
	if err := normalizeCreate(&female); err != nil {
		t.Fatal(err)
	}
	if female.Face != 21700 || female.Hair != 31002 || female.Top != 1041002 {
		t.Fatal("incorrect female defaults")
	}
	for name, mutate := range map[string]func(*domain.CreateCharacterRequest){
		"unknown same-category item": func(r *domain.CreateCharacterRequest) { r.Weapon = 1322999 },
		"custom hair":                func(r *domain.CreateCharacterRequest) { r.Hair = 46830 },
		"other gender face":          func(r *domain.CreateCharacterRequest) { r.Face = 21700 },
		"invalid name":               func(r *domain.CreateCharacterRequest) { r.Name = "bad\x00name" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := male
			mutate(&bad)
			if err := normalizeCreate(&bad); !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("invalid creation accepted: %v", err)
			}
		})
	}
}

func TestPasswordHashAndMalformedParameters(t *testing.T) {
	ctx := context.Background()
	hash, err := hashPassword(ctx, "TestPassword145")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(ctx, hash, "TestPassword145") || verifyPassword(ctx, hash, "wrong") {
		t.Fatal("password verification mismatch")
	}
	if verifyPassword(ctx, "$argon2id$v=19$m=4294967295,t=99,p=99$bad$bad", "x") {
		t.Fatal("unbounded hash parameters accepted")
	}
}
