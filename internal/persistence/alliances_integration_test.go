package persistence

import (
	"context"
	"errors"
	"testing"

	"maple-go-server/internal/domain"
)

func twoGuilds(t *testing.T) (context.Context, *Store, []domain.CharacterSession, int64, int64) {
	t.Helper()
	ctx, s, actors := guildFixture(t)
	var ids [2]int64
	for i, name := range []string{"Alpha", "Bravo"} {
		r, err := s.MutateGuild(ctx, domain.GuildRequest{Session: actors[i], OperationID: guildOp("make" + name), Action: domain.GuildCreate, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		actors[i].Character = *r.Character
		ids[i] = r.After.ID
	}
	return ctx, s, actors, ids[0], ids[1]
}

func TestGuildGPSkillsAndCapacity(t *testing.T) {
	ctx, s, actors, gid, _ := twoGuilds(t)
	a := actors[0]
	r, err := s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("gp1"), Action: domain.GuildGainGP, Amount: 20, Period: "hour-1"})
	if err != nil || r.After.GP != 20 {
		t.Fatalf("gain gp %+v %v", r.After, err)
	}
	if m, _ := r.After.Member(a.CharacterID); m.Contribution != 20 {
		t.Fatalf("contribution %d", m.Contribution)
	}
	if _, err = s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("gp2"), Action: domain.GuildGainGP, Amount: 20, Period: "hour-1"}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("same period granted twice: %v", err)
	}
	// 91000000 level 1 needs guild level 1 and costs 160*10000 mesos.
	r, err = s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("skill"), Action: domain.GuildSkillBuy, SkillID: 91000000})
	if err != nil || r.Skill == nil || r.Skill.Level != 1 || r.Skill.ExpiresAt <= 0 || r.Character == nil || r.Character.Mesos != 2000000-1600000 || r.After.GP != 50 {
		t.Fatalf("skill buy %+v skill=%+v %v", r.After, r.Skill, err)
	}
	a.Character = *r.Character
	// Level 2 needs guild level 3; this guild is still level 1.
	if _, err = s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("skill2"), Action: domain.GuildSkillBuy, SkillID: 91000000}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("level gate: %v", err)
	}
	if _, err = s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("renew"), Action: domain.GuildSkillRenew, SkillID: 91000000}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("renew while active: %v", err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE game_guild_skills SET expires_at=1 WHERE guild_id=$1`, gid); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE game_characters SET mesos=mesos+5000000,revision=revision+1 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	refresh(t, ctx, s, &a)
	r, err = s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("renew2"), Action: domain.GuildSkillRenew, SkillID: 91000000})
	if err != nil || r.Skill.ExpiresAt <= 1 || r.Character.Mesos != a.Character.Mesos-4000000 {
		t.Fatalf("renew %+v %v", r.Skill, err)
	}
	a.Character = *r.Character
	r, err = s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("cap"), Action: domain.GuildCapacity})
	if err != nil || r.After.Capacity != 15 {
		t.Fatalf("capacity %+v %v", r.After, err)
	}
	if _, err = s.MutateGuild(ctx, domain.GuildRequest{Session: a, OperationID: guildOp("capgp"), Action: domain.GuildCapacity, UseGP: true}); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("gp capacity without GP: %v", err)
	}
	if domain.GuildLevelFor(19999) != 1 || domain.GuildLevelFor(20000) != 2 || domain.GuildLevelFor(99999999) != 10 {
		t.Fatal("guild level table")
	}
}

func TestAllianceLifecycle(t *testing.T) {
	ctx, s, actors, ga, gb := twoGuilds(t)
	a, b := actors[0], actors[1]
	created, err := s.MutateAlliance(ctx, domain.AllianceRequest{Session: a, OperationID: guildOp("al"), Action: domain.AllianceCreate, Name: "聯盟", PartnerID: b.CharacterID})
	if err != nil || created.After.ID == 0 || len(created.After.Guilds) != 2 || created.After.LeaderID != a.CharacterID {
		t.Fatalf("create %+v %v", created.After, err)
	}
	ranks := map[int64]int{}
	for _, m := range created.After.Members() {
		ranks[m.CharacterID] = m.AllianceRank
	}
	if ranks[a.CharacterID] != 1 || ranks[b.CharacterID] != 2 {
		t.Fatalf("alliance ranks %+v", ranks)
	}
	read, err := s.ReadAlliance(ctx, b)
	if err != nil || read.ID != created.After.ID {
		t.Fatalf("read %+v %v", read, err)
	}
	if _, err = s.MutateAlliance(ctx, domain.AllianceRequest{Session: b, OperationID: guildOp("bn"), Action: domain.AllianceNotice, Notice: "hello"}); err != nil {
		t.Fatalf("rank2 notice %v", err)
	}
	if _, err = s.MutateAlliance(ctx, domain.AllianceRequest{Session: b, OperationID: guildOp("bt"), Action: domain.AllianceTitles, Titles: [5]string{"A", "B", "C", "D", "E"}}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-leader titles: %v", err)
	}
	r, err := s.MutateAlliance(ctx, domain.AllianceRequest{Session: a, OperationID: guildOp("lead"), Action: domain.AllianceLeader, TargetID: b.CharacterID})
	if err != nil || r.After.LeaderID != b.CharacterID {
		t.Fatalf("leader %+v %v", r.After, err)
	}
	r, err = s.MutateAlliance(ctx, domain.AllianceRequest{Session: b, OperationID: guildOp("kick"), Action: domain.AllianceExpel, GuildID: ga})
	if err != nil || len(r.After.Guilds) != 1 || !r.Expelled || r.Guild.Alliance != 0 {
		t.Fatalf("expel %+v %v", r, err)
	}
	inv, err := s.MutateAlliance(ctx, domain.AllianceRequest{Session: b, OperationID: guildOp("inv"), Action: domain.AllianceInvite, Name: a.Character.Name})
	if err != nil || inv.InviteeID != a.CharacterID {
		t.Fatalf("invite %+v %v", inv, err)
	}
	r, err = s.MutateAlliance(ctx, domain.AllianceRequest{Session: a, OperationID: guildOp("acc"), Action: domain.AllianceAccept})
	if err != nil || len(r.After.Guilds) != 2 || r.GuildID != ga {
		t.Fatalf("accept %+v %v", r, err)
	}
	if _, err = s.MutateAlliance(ctx, domain.AllianceRequest{Session: a, OperationID: guildOp("dis-a"), Action: domain.AllianceDisband}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-leader disband: %v", err)
	}
	if _, err = s.MutateGuild(ctx, domain.GuildRequest{Session: b, OperationID: guildOp("gdis"), Action: domain.GuildDisband}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("guild disband inside alliance: %v", err)
	}
	r, err = s.MutateAlliance(ctx, domain.AllianceRequest{Session: b, OperationID: guildOp("dis"), Action: domain.AllianceDisband})
	if err != nil || !r.Disbanded {
		t.Fatalf("disband %+v %v", r, err)
	}
	g, err := s.ReadGuild(ctx, a)
	if err != nil || g.Alliance != 0 || g.Members[0].AllianceRank != 5 {
		t.Fatalf("guild after disband %+v %v", g, err)
	}
	if _, err = s.MutateGuild(ctx, domain.GuildRequest{Session: b, OperationID: guildOp("gdis2"), Action: domain.GuildDisband}); err != nil {
		t.Fatalf("guild disband: %v", err)
	}
	_ = gb
}
