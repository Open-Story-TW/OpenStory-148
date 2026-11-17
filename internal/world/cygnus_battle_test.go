package world

import (
	"testing"
	"time"

	"maple-go-server/internal/content"
)

func TestCygnusRequiresBothKnightWaves(t *testing.T) {
	g := &gameplaySpace{nextID: 1000000}
	stage := &gameplayRoom{mobs: map[int32]*gameplayMob{}}
	fight := &cygnusBattle{stats: map[int]content.Mob{}, defendingHP: 1050}
	for i := range fight.knights {
		fight.knights[i] = cygnusKnightStart + i
		fight.stats[cygnusKnightStart+i] = content.Mob{ID: cygnusKnightStart + i, HP: 600}
		fight.stats[cygnusKnightStart+cygnusKnightCount+i] = content.Mob{ID: cygnusKnightStart + cygnusKnightCount + i, HP: 1650}
	}
	fight.intro = g.newCygnusMob(content.Mob{ID: cygnusIntro, HP: 40}, -363)
	fight.intro.fake = true
	fight.body = g.newCygnusMob(content.Mob{ID: cygnusBody, HP: 2100}, -363)
	stage.cygnus = fight
	begin := time.Now()
	g.advanceCygnusLocked(stage, begin)
	g.advanceCygnusLocked(stage, begin.Add(cygnusIntroTime-time.Millisecond))
	if len(stage.mobs) != 1 || stage.mobs[fight.intro.objectID] != fight.intro || !fight.intro.fake {
		t.Fatal("the opening animation permitted an early attack")
	}

	start := begin.Add(cygnusIntroTime)
	for i, id := range fight.knights {
		cast := start.Add(time.Duration(i) * cygnusKnightInterval)
		g.advanceCygnusLocked(stage, cast)
		if fight.regular == nil || fight.regular.stats.ID != id || fight.body.lethalHP() != 0 || stage.mobs[fight.intro.objectID] != nil {
			t.Fatalf("knight %d did not protect Cygnus", id)
		}
		g.advanceCygnusLocked(stage, cast.Add(cygnusKnightInterval))
		if fight.regular.stats.ID != id {
			t.Fatal("two ordinary knights spawned at once")
		}
		fight.regular.hp = 0
		g.advanceCygnusLocked(stage, cast.Add(cygnusKnightInterval-time.Millisecond))
		if fight.summoned != i+1 || fight.body.lethalHP() != 1050 || len(fight.awakened) != 0 {
			t.Fatalf("fight progressed before its next cast threshold: knight %d", id)
		}
	}
	now := start.Add(5 * cygnusKnightInterval)
	fight.body.hp = 1050
	g.advanceCygnusLocked(stage, now)
	if len(fight.awakened) != cygnusKnightCount || fight.body.lethalHP() != 0 {
		t.Fatal("half HP did not summon all awakened knights")
	}
	for _, knight := range fight.awakened {
		if fight.body.lethalHP() != 0 {
			t.Fatal("Cygnus became vulnerable with awakened knights still standing")
		}
		knight.hp = 0
		g.advanceCygnusLocked(stage, now)
	}
	if fight.body.lethalHP() != fight.body.hp || fight.body.hp != 1050 || len(fight.awakened) != cygnusKnightCount {
		t.Fatal("defeating the awakened knights did not allow the final hit")
	}
}
