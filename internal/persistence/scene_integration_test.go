package persistence

import (
	"errors"
	"maple-go-server/internal/domain"
	"testing"
)

func TestSceneOwnerFreshDurableAuthorityAndIsolation(t *testing.T) {
	useCombatSchema(t)
	ctx, store, actors := inventoryFixture(t)
	current := actors[0]
	if err := store.ValidateSceneOwner(ctx, current); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetCharacter(ctx, current.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*domain.CharacterSession){
		"account": func(v *domain.CharacterSession) { v.AccountID = actors[1].AccountID },
		"character": func(v *domain.CharacterSession) {
			v.CharacterID = actors[1].CharacterID
			v.Character.ID = v.CharacterID
		},
		"session":    func(v *domain.CharacterSession) { v.SessionID = "scene-wrong-session" },
		"generation": func(v *domain.CharacterSession) { v.Generation++ },
		"channel":    func(v *domain.CharacterSession) { v.ChannelID++ },
		"world":      func(v *domain.CharacterSession) { v.Lease.WorldID++ },
		"instance":   func(v *domain.CharacterSession) { v.Lease.InstanceID += "-wrong" },
		"epoch":      func(v *domain.CharacterSession) { v.Lease.Epoch++ },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			v := current
			change(&v)
			if e := store.ValidateSceneOwner(ctx, v); !errors.Is(e, domain.ErrStaleOwner) {
				t.Fatalf("wrong authority accepted: %v", e)
			}
		})
	}
	// Changing only mutable position/revision does not create a new owner.
	mutable := current
	mutable.Character.MapID = 40000
	mutable.Character.Revision += 99
	if err = store.ValidateSceneOwner(ctx, mutable); err != nil {
		t.Fatal(err)
	}
	after, err := store.GetCharacter(ctx, current.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || after.MapID != before.MapID || after.HP != before.HP {
		t.Fatal("validator mutated character")
	}
	// A delayed initial claim must not install itself after the durable owner
	// has released, even when no replacement currently appears in local memory.
	if err = store.ReleaseCharacter(ctx, current); err != nil {
		t.Fatal(err)
	}
	if err = store.ValidateSceneOwner(ctx, current); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("released owner accepted: %v", err)
	}
	if err = store.ValidateSceneOwner(ctx, actors[1]); err != nil {
		t.Fatal("other account affected", err)
	}
}

func TestSceneOwnerRejectsBothIndependentDurableFences(t *testing.T) {
	useCombatSchema(t)
	ctx, store, actors := inventoryFixture(t)
	v := actors[0]
	// Each row is checked independently; a valid account session cannot excuse
	// missing/wrong character ownership, and vice versa.
	for _, test := range []struct{ name, table, column string }{
		{"account_state", "ops_account_sessions", "state"},
		{"character_channel", "ops_character_ownership", "channel_id"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var alter, restore string
			switch test.column {
			case "state":
				alter = "UPDATE ops_account_sessions SET state='login' WHERE account_id=$1"
				restore = "UPDATE ops_account_sessions SET state='world' WHERE account_id=$1"
			case "channel_id":
				alter = "UPDATE ops_character_ownership SET channel_id=channel_id+1 WHERE account_id=$1"
				restore = "UPDATE ops_character_ownership SET channel_id=channel_id-1 WHERE account_id=$1"
			}
			if _, err := store.db.ExecContext(ctx, alter, v.AccountID); err != nil {
				t.Fatal(err)
			}
			err := store.ValidateSceneOwner(ctx, v)
			if _, e := store.db.ExecContext(ctx, restore, v.AccountID); e != nil {
				t.Fatal(e)
			}
			if !errors.Is(err, domain.ErrStaleOwner) {
				t.Fatalf("independent fence bypassed: %v", err)
			}
		})
	}
	if err := store.ValidateSceneOwner(ctx, v); err != nil {
		t.Fatal(err)
	}
}
