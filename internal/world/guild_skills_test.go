package world

import (
	"testing"

	"maple-go-server/internal/domain"
)

func TestGuildSkillPurchaseAndLeaveUpdateOnlineMembers(t *testing.T) {
	g := domain.GuildSnapshot{ID: 4, Name: "Guild", Members: []domain.GuildMemberInfo{{CharacterID: 1, Rank: 1}, {CharacterID: 2, Rank: 5}}}
	makePeer := func(id int64) guildPeer {
		return guildPeer{live: &liveCharacter{session: domain.CharacterSession{CharacterID: id, Character: domain.Character{ID: id, Guild: g.Tag(id)}}}}
	}
	space := &guildSpace{peers: map[int64]guildPeer{1: makePeer(1), 2: makePeer(2), 3: makePeer(3)}}
	purchased := g
	purchased.Skills = []domain.GuildSkillState{{SkillID: 91000002, Level: 1, ExpiresAt: 200}}
	if updates := installGuildState(space, g, purchased); len(updates) != 0 {
		t.Fatal("skill purchase republished unchanged guild marks")
	}
	for _, id := range []int64{1, 2} {
		if got := space.peers[id].live.session.Character.GuildSkills; len(got) != 1 || got[0] != purchased.Skills[0] {
			t.Fatalf("guild member %d did not get the paid rank: %+v", id, got)
		}
	}
	if got := space.peers[3].live.session.Character.GuildSkills; len(got) > 0 {
		t.Fatalf("unrelated character got guild rank: %+v", got)
	}
	retained := purchased
	retained.Members = retained.Members[:1]
	if updates := installGuildState(space, purchased, retained); len(updates) != 1 || updates[0].tag.ID != 0 {
		t.Fatalf("leaving member did not clear its guild mark: %+v", updates)
	}
	if got := space.peers[2].live.session.Character.GuildSkills; len(got) > 0 {
		t.Fatalf("leaving member kept paid ranks: %+v", got)
	}
}
