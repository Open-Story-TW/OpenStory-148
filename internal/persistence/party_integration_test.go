package persistence

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func partyFixture(t *testing.T) (context.Context, *Store, []domain.CharacterSession) {
	t.Helper()
	ctx, s, actors := inventoryFixture(t)
	for i := range actors {
		if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET level=10 WHERE id=$1`, actors[i].CharacterID); err != nil {
			t.Fatal(err)
		}
		actors[i].Character.Level = 10
	}
	return ctx, s, actors
}

func partyExtraActor(t *testing.T, ctx context.Context, s *Store, lease domain.WorldLease, n int) domain.CharacterSession {
	t.Helper()
	suffix, err := randomToken(4)
	if err != nil {
		t.Fatal(err)
	}
	account, err := s.Register(ctx, "pt"+suffix, "TestPassword145")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		clean, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		for _, table := range []string{"ops_transfers", "ops_character_ownership", "ops_account_sessions", "ops_outbox", "game_character_roster", "game_characters", "auth_accounts"} {
			column := "account_id"
			if table == "auth_accounts" {
				column = "id"
			}
			if _, e := s.db.ExecContext(clean, "DELETE FROM "+table+" WHERE "+column+"=$1", account.ID); e != nil {
				t.Errorf("party cleanup %s: %v", table, e)
			}
		}
	})
	if err = s.ConfigureAccount(ctx, account.ID, 0, "145145"); err != nil {
		t.Fatal(err)
	}
	login, err := s.BeginLogin(ctx, account.ID, "party-login-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateCharacter(ctx, domain.CreateCharacterRequest{LoginSession: login, Lease: lease, AccountID: account.ID, WorldID: lease.WorldID, Name: fmt.Sprintf("PT%d%s", n, suffix), Gender: 0, Face: 20100, Hair: 30000, Top: 1040010, Bottom: 1060002, Shoes: 1072005, Weapon: 1322005, OperationID: "party-create-" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := s.CreateTransfer(ctx, domain.TransferRequest{AccountID: account.ID, CharacterID: c.ID, WorldID: lease.WorldID, ChannelID: 1, SessionID: login.SessionID, ClientIP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := s.ConsumeTransfer(ctx, domain.TransferClaim{ClientHandle: tr.ClientHandle, WorldID: lease.WorldID, ChannelID: 1, ClientIP: "127.0.0.1", Lease: lease, AllowLegacyLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	return actor
}

func TestPartyDurableInviteReplyFencingAndLeadership(t *testing.T) {
	ctx, s, actors := partyFixture(t)
	a, b := actors[0], actors[1]
	if _, err := s.MutateParty(ctx, domain.PartyRequest{Session: b, Action: domain.PartyAccept, PartyID: 123456789}); !errors.Is(err, domain.ErrPartyInvite) {
		t.Fatal("forged accept", err)
	}
	created, err := s.MutateParty(ctx, domain.PartyRequest{Session: a, Action: domain.PartyCreate})
	if err != nil {
		t.Fatal(err)
	}
	id := created.After.ID
	duplicate, err := s.MutateParty(ctx, domain.PartyRequest{Session: a, Action: domain.PartyCreate})
	if err != nil || duplicate.Created || duplicate.After.ID != id || duplicate.After.Revision != created.After.Revision {
		t.Fatalf("duplicate create %+v %v", duplicate, err)
	}
	invite := domain.PartyRequest{Session: a, Action: domain.PartyInvite, Name: b.Character.Name, TargetID: b.CharacterID}
	badWorld := invite
	badWorld.Session.Lease.WorldID++
	if _, err = s.MutateParty(ctx, badWorld); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("cross-world actor", err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE ops_account_sessions SET state='cashshop' WHERE account_id=$1`, b.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MutateParty(ctx, invite); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("cash/offline target", err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE ops_account_sessions SET state='world' WHERE account_id=$1`, b.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE game_characters SET world_id=123 WHERE id=$1`, b.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MutateParty(ctx, invite); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("cross-world target", err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE game_characters SET world_id=124 WHERE id=$1`, b.CharacterID); err != nil {
		t.Fatal(err)
	}
	got, err := s.MutateParty(ctx, invite)
	if err != nil || got.Invitation == nil {
		t.Fatalf("invite %+v %v", got, err)
	}
	reloaded, err := s.ReadParty(ctx, b)
	if err != nil || len(reloaded.Invitations) != 1 || reloaded.Party.ID != 0 {
		t.Fatalf("offline pending %+v %v", reloaded, err)
	}
	for _, action := range []domain.PartyAction{domain.PartyAcknowledge, domain.PartyPending, domain.PartyDuplicate} {
		got, err = s.MutateParty(ctx, domain.PartyRequest{Session: b, Action: action, PartyID: id})
		if err != nil || got.After.ID != 0 {
			t.Fatalf("ack %d joined %+v %v", action, got, err)
		}
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE game_party_invitations SET expires_at=clock_timestamp()-interval '1 second' WHERE party_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MutateParty(ctx, domain.PartyRequest{Session: b, Action: domain.PartyAccept, PartyID: id}); !errors.Is(err, domain.ErrPartyInvite) {
		t.Fatal("expired invite", err)
	}
	if _, err = s.MutateParty(ctx, invite); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MutateParty(ctx, domain.PartyRequest{Session: b, Action: domain.PartyDeny, PartyID: id}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MutateParty(ctx, domain.PartyRequest{Session: b, Action: domain.PartyAccept, PartyID: id}); !errors.Is(err, domain.ErrPartyInvite) {
		t.Fatal("denied invite", err)
	}
	if _, err = s.MutateParty(ctx, invite); err != nil {
		t.Fatal(err)
	}
	bad := domain.PartyRequest{Session: b, Action: domain.PartyAccept, PartyID: id}
	bad.Session.Generation++
	if _, err = s.MutateParty(ctx, bad); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("stale generation", err)
	}
	bad.Session = b
	bad.Session.Lease.Epoch++
	if _, err = s.MutateParty(ctx, bad); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("stale lease", err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.MutateParty(ctx, domain.PartyRequest{Session: b, Action: domain.PartyAccept, PartyID: id}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	state, err := s.ReadParty(ctx, a)
	if err != nil || len(state.Party.Members) != 2 || state.Party.Revision != 2 {
		t.Fatalf("replayed join %+v %v", state, err)
	}
	for _, m := range state.Party.Members {
		if m.Channel != 0 || m.MapID != 0 || m.HP != 0 || m.MaxHP != 0 {
			t.Fatal("SQL leaked stale live state", m)
		}
	}
	for _, action := range []domain.PartyAction{domain.PartyExpel, domain.PartyLeader, domain.PartySetting} {
		if _, err = s.MutateParty(ctx, domain.PartyRequest{Session: b, Action: action, TargetID: a.CharacterID}); !errors.Is(err, domain.ErrForbidden) {
			t.Fatal("nonleader", action, err)
		}
	}
	got, err = s.MutateParty(ctx, domain.PartyRequest{Session: a, Action: domain.PartyLeader, TargetID: b.CharacterID})
	if err != nil || got.After.LeaderID != b.CharacterID || got.After.Revision != 3 {
		t.Fatalf("leader %+v %v", got, err)
	}
	if _, err = s.MutateParty(ctx, domain.PartyRequest{Session: a, Action: domain.PartyExpel, TargetID: b.CharacterID}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("old leader", err)
	}
	got, err = s.MutateParty(ctx, domain.PartyRequest{Session: b, Action: domain.PartyExpel, TargetID: a.CharacterID})
	if err != nil || len(got.After.Members) != 1 {
		t.Fatalf("expel %+v %v", got, err)
	}
	got, err = s.MutateParty(ctx, domain.PartyRequest{Session: b, Action: domain.PartyLeave})
	if err != nil || got.After.ID != 0 {
		t.Fatalf("leader disband %+v %v", got, err)
	}
	state, err = s.ReadParty(ctx, a)
	if err != nil || state.Party.ID != 0 {
		t.Fatal("disband retained membership", state, err)
	}
}

func TestPartySixMemberRaceInviteEpochAndRollback(t *testing.T) {
	ctx, s, actors := partyFixture(t)
	a := actors[0]
	for n := 2; n < 8; n++ {
		actors = append(actors, partyExtraActor(t, ctx, s, a.Lease, n))
	}
	created, err := s.MutateParty(ctx, domain.PartyRequest{Session: a, Action: domain.PartyCreate})
	if err != nil {
		t.Fatal(err)
	}
	id := created.After.ID
	for _, target := range actors[1:] {
		if _, err = s.MutateParty(ctx, domain.PartyRequest{Session: a, Action: domain.PartyInvite, TargetID: target.CharacterID, Name: target.Character.Name}); err != nil {
			t.Fatal(err)
		}
	}
	var joined, full atomic.Int32
	var wg sync.WaitGroup
	for _, target := range actors[1:] {
		wg.Add(1)
		go func(target domain.CharacterSession) {
			defer wg.Done()
			_, err := s.MutateParty(ctx, domain.PartyRequest{Session: target, Action: domain.PartyAccept, PartyID: id})
			if err == nil {
				joined.Add(1)
			} else if errors.Is(err, domain.ErrPartyFull) {
				full.Add(1)
			} else {
				t.Error(err)
			}
		}(target)
	}
	wg.Wait()
	if joined.Load() != 5 || full.Load() != 2 {
		t.Fatalf("joined%d full%d", joined.Load(), full.Load())
	}
	state, err := s.ReadParty(ctx, a)
	if err != nil || len(state.Party.Members) != 6 || state.Party.Revision != 6 {
		t.Fatalf("capacity %+v %v", state, err)
	}
	// Changing leader invalidates still-pending invitations, even if a slot
	// later opens. The old leader cannot hand out a new epoch's authority.
	member := state.Party.Members[1].CharacterID
	var next domain.CharacterSession
	var outsider domain.CharacterSession
	for _, actor := range actors {
		if actor.CharacterID == member {
			next = actor
		}
		if _, ok := state.Party.Member(actor.CharacterID); !ok {
			outsider = actor
		}
	}
	if _, err = s.MutateParty(ctx, domain.PartyRequest{Session: a, Action: domain.PartyLeader, TargetID: next.CharacterID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MutateParty(ctx, domain.PartyRequest{Session: a, Action: domain.PartyLeave}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MutateParty(ctx, domain.PartyRequest{Session: outsider, Action: domain.PartyAccept, PartyID: id}); !errors.Is(err, domain.ErrPartyInvite) {
		t.Fatal("old leader invitation accepted", err)
	}
	// An injected insert failure must roll back automatic party creation too.
	name := fmt.Sprintf("party_fail_%d", a.CharacterID)
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'party invitation test failure'; END $$; CREATE TRIGGER %s BEFORE INSERT ON game_party_invitations FOR EACH ROW EXECUTE FUNCTION %s()`, name, name, name))
	if err != nil {
		t.Fatal(err)
	}
	clean := func() {
		_, _ = s.db.ExecContext(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON game_party_invitations; DROP FUNCTION IF EXISTS %s()", name, name))
	}
	t.Cleanup(clean)
	_, err = s.MutateParty(ctx, domain.PartyRequest{Session: a, Action: domain.PartyInvite, TargetID: outsider.CharacterID, Name: outsider.Character.Name})
	if err == nil {
		t.Fatal("injected failure ignored")
	}
	clean()
	state, err = s.ReadParty(ctx, a)
	if err != nil || state.Party.ID != 0 {
		t.Fatal("automatic party creation escaped rollback", state, err)
	}
	// The target must match the name and this runtime's live world ownership.
	if _, err = s.MutateParty(ctx, domain.PartyRequest{Session: a, Action: domain.PartyInvite, TargetID: outsider.CharacterID, Name: a.Character.Name}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("substituted target", err)
	}
}
