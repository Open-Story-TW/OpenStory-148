package world

import (
	"testing"

	"maple-go-server/internal/content"
)

func TestKrexelOnlyAdvancesAfterTheActiveEyeDies(t *testing.T) {
	key := mapKey{channel: 1, mapID: content.KrexelArena}
	first := &gameplayMob{objectID: 1, hp: 250_000_000, stats: content.Mob{ID: 9420521}}
	second := &gameplayMob{objectID: 2, hp: 250_000_000, stats: content.Mob{ID: 9420522}}
	battle := &krexelBattle{
		altar: &roomReactor{state: krexelLastReactorState},
		forms: [3]*gameplayMob{nil, first, second},
		stage: 1,
	}
	room := &gameplayRoom{krexel: battle, mobs: map[int32]*gameplayMob{first.objectID: first}}
	s := &Server{gameplay: &gameplaySpace{rooms: map[mapKey]*gameplayRoom{key: room}}}
	s.krexelDefeated(key, second.stats.ID)
	s.krexelDefeated(key, first.stats.ID)
	if battle.stage != 1 || room.mobs[second.objectID] != nil {
		t.Fatal("a living eye or the wrong template advanced the fight")
	}
	first.hp = 0
	s.krexelDefeated(key, first.stats.ID)
	if battle.stage != 2 || room.mobs[second.objectID] != second {
		t.Fatal("the first eye did not release the second eye")
	}
	second.hp = 0
	s.krexelDefeated(key, second.stats.ID)
	if battle.stage != 3 {
		t.Fatal("the final eye did not clear the fight")
	}
	room.resetKrexel()
	if room.krexel != nil || battle.altar.state != 0 || len(room.mobs) != 0 {
		t.Fatal("the field did not reset on empty")
	}
}
