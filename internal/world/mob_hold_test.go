package world

import (
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func TestHeldMobKeepsOneHPAndRefills(t *testing.T) {
	if name, args, _ := parseGMChat("!mobhold 1"); name != "mobhold" || len(args) != 1 || args[0] != 1 {
		t.Fatalf("parse = %q %v", name, args)
	}
	mob := &gameplayMob{objectID: 7, stats: content.Mob{HP: 100}, hp: 100}
	g := &gameplaySpace{rooms: map[mapKey]*gameplayRoom{{channel: 1, mapID: 104010000}: {members: map[int64]*gameplayMember{}, mobs: map[int32]*gameplayMob{7: mob}}}}
	s := &Server{gameplay: g}
	if mob.lethalHP() != 100 {
		t.Fatal("unheld mob must stay lethal")
	}
	s.holdFieldMobs(mapKey{channel: 1, mapID: 104010000}, true)
	if mob.lethalHP() != 99 {
		t.Fatalf("held lethal = %d", mob.lethalHP())
	}
	mob.hp, mob.contributions = 1, []domain.CombatContribution{{CharacterID: 1, Damage: 99}}
	mob.claiming = true
	g.refillHeldMobs()
	if mob.hp != 1 {
		t.Fatal("refilled during a pending settlement")
	}
	mob.claiming = false
	g.refillHeldMobs()
	if mob.hp != 100 || mob.contributions != nil {
		t.Fatalf("refill = %d %v", mob.hp, mob.contributions)
	}
	s.holdFieldMobs(mapKey{channel: 1, mapID: 104010000}, false)
	if mob.lethalHP() != 100 {
		t.Fatal("release must restore lethality")
	}
}
