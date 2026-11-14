package world

import (
	"testing"
	"time"

	"maple-go-server/internal/content"
)

func TestAttackKeyIsUniquePerLiveCharacter(t *testing.T) {
	var live liveCharacter
	now := time.UnixMilli(1_700_000_000_000)
	first := live.nextAttackKey(now)
	// Keydown frames arrive with one client tick; the same server instant
	// must still hand out distinct keys.
	second := live.nextAttackKey(now)
	third := live.nextAttackKey(now.Add(-time.Second))
	if first == second || second == third || second != first+1 || third != second+1 {
		t.Fatalf("keys = %d %d %d", first, second, third)
	}
	if later := live.nextAttackKey(now.Add(time.Minute)); later <= third {
		t.Fatalf("later key %d <= %d", later, third)
	}
}

func TestAttackIntervalAdmitsRapidFire(t *testing.T) {
	if attackInterval(3121004) != 50*time.Millisecond || attackInterval(23121000) != 50*time.Millisecond {
		t.Fatal("hurricane-type skills keep the 200ms gap")
	}
	if attackInterval(0) != 200*time.Millisecond || attackInterval(1001004) != 200*time.Millisecond {
		t.Fatal("ordinary attacks lost the 200ms gap")
	}
}

func TestHeldMobReleasesAfterTerm(t *testing.T) {
	mob := &gameplayMob{objectID: 7, stats: content.Mob{HP: 100}, hp: 100}
	g := &gameplaySpace{rooms: map[mapKey]*gameplayRoom{{channel: 1, mapID: 104010000}: {members: map[int64]*gameplayMember{}, mobs: map[int32]*gameplayMob{7: mob}}}}
	s := &Server{gameplay: g}
	start := time.Now()
	s.holdFieldMobsAt(mapKey{channel: 1, mapID: 104010000}, true, start)
	g.refillHeldMobsAt(start.Add(mobHoldTerm - time.Second))
	if mob.lethalHP() != 99 {
		t.Fatal("hold released early")
	}
	mob.hp = 1
	g.refillHeldMobsAt(start.Add(mobHoldTerm))
	if mob.qaHeld || mob.lethalHP() != 1 {
		t.Fatalf("hold still active after its term: held %v lethal %d", mob.qaHeld, mob.lethalHP())
	}
}
