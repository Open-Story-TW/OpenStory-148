package persistence

import (
	"errors"
	"fmt"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func TestScriptTravelCommitsFareAndDestinationTogether(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	catalog, err := content.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	dest, err := catalog.Map(20000)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := dest.Spawn()
	if !ok {
		t.Fatal("missing destination portal")
	}
	travel := func(i int) *domain.ScriptTravel {
		return &domain.ScriptTravel{FromMap: sessions[i].Character.MapID, MapID: dest.ID, SpawnPoint: entry.ID, X: entry.X, Y: entry.Y}
	}
	r := domain.ScriptExchange{Session: sessions[0], OperationID: fmt.Sprintf("test-script-travel-fare-%d", sessions[0].CharacterID), MesosDelta: -150, Travel: travel(0)}
	result, err := s.ExecuteScriptExchange(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if result.Character.Mesos != 850 || result.Character.MapID != dest.ID || result.Character.SpawnPoint != entry.ID || result.Character.X != entry.X || result.Character.Y != entry.Y {
		t.Fatalf("travel %+v", result.Character)
	}
	stored, err := s.GetCharacter(ctx, r.Session.CharacterID)
	if err != nil || stored.MapID != dest.ID || stored.Mesos != 850 {
		t.Fatal(stored, err)
	}
	replay, err := s.ExecuteScriptExchange(ctx, r)
	if err != nil || !replay.Replayed || replay.Character.Mesos != 850 {
		t.Fatal(replay, err)
	}
	other := r
	copyTravel := *r.Travel
	copyTravel.FromMap++
	other.Travel = &copyTravel
	if _, err = s.ExecuteScriptExchange(ctx, other); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("forged source %v", err)
	}
	copyTravel = *r.Travel
	copyTravel.X++
	other.Travel = &copyTravel
	if _, err = s.ExecuteScriptExchange(ctx, other); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("forged portal %v", err)
	}
	for i, failed := range []domain.ScriptExchange{
		{Session: sessions[1], MesosDelta: -1001, Travel: travel(1)},
		{Session: sessions[1], Items: []domain.ScriptItemDelta{{TemplateID: 2000000, Quantity: -100}}, Travel: travel(1)},
	} {
		failed.OperationID = fmt.Sprintf("travel-failure-%d-%d", sessions[1].CharacterID, i)
		if _, err = s.ExecuteScriptExchange(ctx, failed); !errors.Is(err, domain.ErrInsufficient) {
			t.Fatalf("failure%d=%v", i, err)
		}
		stored, err = s.GetCharacter(ctx, sessions[1].CharacterID)
		if err != nil || stored.MapID != sessions[1].Character.MapID || stored.Mesos != 1000 || itemQuantity(stored, 2000000) != 5 {
			t.Fatalf("partial travel %+v %v", stored, err)
		}
	}
	token := domain.ScriptExchange{Session: sessions[1], OperationID: fmt.Sprintf("test-script-travel-token-%d", sessions[1].CharacterID), Items: []domain.ScriptItemDelta{{TemplateID: 2000000, Quantity: -1}}, Travel: travel(1)}
	result, err = s.ExecuteScriptExchange(ctx, token)
	if err != nil || result.Character.MapID != dest.ID || result.Character.Mesos != 1000 || itemQuantity(result.Character, 2000000) != 4 {
		t.Fatalf("token travel %+v %v", result, err)
	}
}
