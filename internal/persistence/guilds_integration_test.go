package persistence

import (
	"context"
	"errors"
	"sync"
	"testing"

	"maple-go-server/internal/domain"
)

func guildFixture(t *testing.T) (context.Context, *Store, []domain.CharacterSession) {
	t.Helper()
	usePrivateSchema(t, "guild")
	ctx, s, actors := inventoryFixture(t)
	for i := range actors {
		if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET mesos=7000000,map_id=$2 WHERE id=$1`, actors[i].CharacterID, domain.GuildOfficeMapID); err != nil {
			t.Fatal(err)
		}
		var err error
		if actors[i].Character, err = s.GetCharacter(ctx, actors[i].CharacterID); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, s, actors
}

func guildOp(name string) string { return "guild-test-" + name + "-0123456789" }

func refresh(t *testing.T, ctx context.Context, s *Store, a *domain.CharacterSession) {
	t.Helper()
	c, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	a.Character = c
}

func TestGuildCreateChargesOnceAndRejectsDuplicateName(t *testing.T) {
	ctx, s, actors := guildFixture(t)
	a, b := actors[0], actors[1]
	away := a
	away.Character.MapID = 100000000
	if _, err := s.MutateGuild(ctx, domain.GuildRequest{Session: away, OperationID: guildOp("away"), Action: domain.GuildCreate, Name: "Heroes"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("outside guild office: %v", err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("create" + string(rune('a'+i))), Action: domain.GuildCreate, Name: "Heroes"})
			if err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("parallel creates committed %d", wins)
	}
	refresh(t, ctx, s, &a)
	if a.Character.Mesos != 2000000 || a.Character.Guild.Name != "Heroes" || a.Character.Guild.Rank != 1 {
		t.Fatalf("creator after create: mesos=%d guild=%+v", a.Character.Mesos, a.Character.Guild)
	}
	if _, err := s.MutateGuild(ctx, domain.GuildRequest{Session: b, OperationID: guildOp("dupe"), Action: domain.GuildCreate, Name: "heroes"}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("case-insensitive duplicate: %v", err)
	}
	if _, err := s.MutateGuild(ctx, domain.GuildRequest{Session: b, OperationID: guildOp("short"), Action: domain.GuildCreate, Name: "ab"}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("short name: %v", err)
	}
}

func TestGuildInviteAcceptRanksNoticeEmblemLeaderLeave(t *testing.T) {
	ctx, s, actors := guildFixture(t)
	a, b := actors[0], actors[1]
	created, err := s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("c"), Action: domain.GuildCreate, Name: "楓之谷"})
	if err != nil || created.After.ID == 0 || len(created.After.Members) != 1 || created.Character == nil {
		t.Fatalf("create %+v %v", created, err)
	}
	a.Character = *created.Character
	gid := created.After.ID
	if _, err = s.MutateGuild(ctx, domain.GuildRequest{Session: b, OperationID: guildOp("forged"), Action: domain.GuildAccept, GuildID: gid}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("accept without invite: %v", err)
	}
	inv, err := s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("inv"), Action: domain.GuildInvite, Name: b.Character.Name})
	if err != nil || inv.Invitation == nil || inv.TargetID != b.CharacterID {
		t.Fatalf("invite %+v %v", inv, err)
	}
	if _, err = s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("inv2"), Action: domain.GuildInvite, Name: b.Character.Name}); !errors.Is(err, domain.ErrGuildInvitePending) {
		t.Fatalf("double invite: %v", err)
	}
	if _, err = s.MutateGuild(ctx, domain.GuildRequest{Session: b, OperationID: guildOp("inv"), Action: domain.GuildAccept, GuildID: gid}); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if _, err = s.MutateGuild(ctx, domain.GuildRequest{Session: b, OperationID: guildOp("inv"), Action: domain.GuildAccept, GuildID: gid}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("accept replay: %v", err)
	}
	g, err := s.ReadGuild(ctx, b)
	if err != nil || len(g.Members) != 2 {
		t.Fatalf("read after accept: %+v %v", g, err)
	}
	if m, _ := g.Member(b.CharacterID); m.Rank != domain.GuildMember {
		t.Fatalf("joined rank %d", m.Rank)
	}
	if _, err = s.MutateGuild(ctx, domain.GuildRequest{Session: b, OperationID: guildOp("notice-b"), Action: domain.GuildNotice, Notice: "hi"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member notice: %v", err)
	}
	r, err := s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("rank"), Action: domain.GuildChangeRank, TargetID: b.CharacterID, Rank: 2})
	if m, _ := r.After.Member(b.CharacterID); err != nil || m.Rank != 2 {
		t.Fatalf("promote %+v %v", r, err)
	}
	r, err = s.MutateGuild(ctx, domain.GuildRequest{Session: b, OperationID: guildOp("notice"), Action: domain.GuildNotice, Notice: "歡迎"})
	if err != nil || r.After.Notice != "歡迎" {
		t.Fatalf("jr notice %+v %v", r, err)
	}
	r, err = s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("titles"), Action: domain.GuildTitles, Titles: [5]string{"王", "副", "甲", "", ""}})
	if err != nil || r.After.Titles[2] != "甲" || r.After.Titles[4] != "" {
		t.Fatalf("titles %+v %v", r, err)
	}
	r, err = s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("emblem"), Action: domain.GuildEmblem, Emblem: domain.GuildEmblemInfo{BG: 1000, BGColor: 3, Logo: 2001, Color: 7}})
	if err != nil || r.After.Emblem.Logo != 2001 || r.Character == nil || r.Character.Mesos != 500000 || r.Character.Guild.Emblem.Logo != 2001 {
		t.Fatalf("emblem %+v %v", r, err)
	}
	a.Character = *r.Character
	if _, err = s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("leave-master"), Action: domain.GuildLeave}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("master leaves populated guild: %v", err)
	}
	r, err = s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("leader"), Action: domain.GuildLeader, TargetID: b.CharacterID})
	if err != nil || r.After.LeaderID != b.CharacterID {
		t.Fatalf("leader %+v %v", r, err)
	}
	if m, _ := r.After.Member(a.CharacterID); m.Rank != 2 {
		t.Fatalf("old leader rank %d", m.Rank)
	}
	r, err = s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("leave-member"), Action: domain.GuildLeave})
	if _, still := r.After.Member(a.CharacterID); err != nil || r.Disbanded || r.After.ID != gid || still || r.After.Tag(b.CharacterID).ID != gid {
		t.Fatalf("member leave keeps remaining guild %+v %v", r, err)
	}
	refresh(t, ctx, s, &a)
	if _, err = s.MutateGuild(ctx, domain.GuildRequest{Session: b, OperationID: guildOp("reinv"), Action: domain.GuildInvite, Name: a.Character.Name}); err != nil {
		t.Fatalf("reinvite: %v", err)
	}
	if _, err = s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("reacc"), Action: domain.GuildAccept, GuildID: gid}); err != nil {
		t.Fatalf("reaccept: %v", err)
	}
	refresh(t, ctx, s, &a)
	r, err = s.MutateGuild(ctx, domain.GuildRequest{Session: b, OperationID: guildOp("expel"), Action: domain.GuildExpel, TargetID: a.CharacterID})
	if err != nil || len(r.After.Members) != 1 || r.TargetName != a.Character.Name {
		t.Fatalf("expel %+v %v", r, err)
	}
	refresh(t, ctx, s, &a)
	if a.Character.Guild.ID != 0 {
		t.Fatalf("expelled tag %+v", a.Character.Guild)
	}
	r, err = s.MutateGuild(ctx, domain.GuildRequest{Session: b, OperationID: guildOp("disband"), Action: domain.GuildLeave})
	if err != nil || !r.Disbanded {
		t.Fatalf("sole master leave disbands %+v %v", r, err)
	}
	var left int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM game_guilds WHERE id=$1`, gid).Scan(&left); err != nil || left != 0 {
		t.Fatalf("guild row remains %d %v", left, err)
	}
}

func TestGuildDenyAndCapacity(t *testing.T) {
	ctx, s, actors := guildFixture(t)
	a, b := actors[0], actors[1]
	created, err := s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("c"), Action: domain.GuildCreate, Name: "Tiny"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("i1"), Action: domain.GuildInvite, Name: b.Character.Name}); err != nil {
		t.Fatal(err)
	}
	d, err := s.MutateGuild(ctx, domain.GuildRequest{Session: b, OperationID: guildOp("deny"), Action: domain.GuildDeny})
	if err != nil || d.Invitation == nil || d.Invitation.InviterID != a.CharacterID {
		t.Fatalf("deny %+v %v", d, err)
	}
	if _, err = s.MutateGuild(ctx, domain.GuildRequest{Session: b, OperationID: guildOp("late"), Action: domain.GuildAccept, GuildID: created.After.ID}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("accept after deny: %v", err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE game_guilds SET capacity=1 WHERE id=$1`, created.After.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("i2"), Action: domain.GuildInvite, Name: b.Character.Name}); err != nil {
		t.Fatal(err)
	}
	// A stored capacity of 1 is raised to the operator's base capacity.
	restore := domain.SetGuildCapacityForTest(domain.DefaultGuildCapacityLimit, 1)
	defer restore()
	if _, err = s.MutateGuild(ctx, domain.GuildRequest{Session: b, OperationID: guildOp("full"), Action: domain.GuildAccept, GuildID: created.After.ID}); !errors.Is(err, domain.ErrGuildFull) {
		t.Fatalf("full guild: %v", err)
	}
	domain.SetGuildCapacity(domain.DefaultGuildCapacityLimit, 2)
	// The refused accept rolled back, so the invitation may still be pending.
	if _, err = s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("i3"), Action: domain.GuildInvite, Name: b.Character.Name}); err != nil && !errors.Is(err, domain.ErrGuildInvitePending) {
		t.Fatal(err)
	}
	joined, err := s.MutateGuild(ctx, domain.GuildRequest{Session: b, OperationID: guildOp("base"), Action: domain.GuildAccept, GuildID: created.After.ID})
	if err != nil || joined.After.Capacity != 2 || len(joined.After.Members) != 2 {
		t.Fatalf("join under base capacity: %+v %v", joined.After, err)
	}
}
