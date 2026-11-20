package world

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestV148MindControlMonsterContact(t *testing.T) {
	s, lives, sinks, _ := sharedWorldFixture(t)
	actor := lives[0]
	actor.session.Character.Skills = []domain.SkillState{{ID: 5921009, Level: 20}}
	seedBoundScene(t, s, actor, sinks[0])
	key := mapKey{channel: 1, mapID: 40000}
	room := s.gameplay.rooms[key]
	room.members[1].visit = actor.visit
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Mob.wz"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"8210002.img.xml", "8210004.img.xml"} {
		const body = `<imgdir><imgdir name="stand"><canvas name="0"><vector name="lt" x="-80" y="-150"/><vector name="rb" x="80" y="0"/></canvas></imgdir></imgdir>`
		if err := os.WriteFile(filepath.Join(root, "Mob.wz", name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	if s.gameplay.catalog, err = content.NewCatalog(root); err != nil {
		t.Fatal(err)
	}
	target := &gameplayMob{objectID: 1000037, life: content.Life{X: -158, Y: 214}, hp: 115000, controller: 1,
		stats: content.Mob{ID: 8210004, HP: 115000, PAD: 2237, PDRate: 10}, statuses: combat.MobStatuses{
			combat.MobDazzle: {Stat: combat.MobDazzle, Value: 1, SkillID: 5921009, OwnerID: 1,
				OwnerSession: actor.session.SessionID, OwnerGeneration: actor.session.Generation, ExpiresAt: time.Now().Add(40 * time.Second)},
		}}
	source := &gameplayMob{objectID: 1000038, life: content.Life{X: -108, Y: 214}, hp: 105000, controller: 1,
		stats: content.Mob{ID: 8210002, HP: 105000, PAD: 1672, PDRate: 10}}
	room.mobs[source.objectID], room.mobs[target.objectID] = source, target
	settled := 0
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		settled++
		if q.SkillID != 5921009 || q.Cost.Opcode != protocol.RecvMonsterContact || q.Cost.MP != 0 || q.Cost.Ammo != 0 ||
			q.Targets[0].Damage != 500 || q.Targets[0].Lines[0].Raw != 1321 || !q.Targets[0].Defeated {
			t.Fatalf("incorrect durable contact %+v", q)
		}
		return fakeSharedResult(q), nil
	}}
	// First genuine R36 wire. Only the reporter ID is replaced for this test fixture.
	wire, err := hex.DecodeString("690166420f002f13000065420f0000ff290500000096ff8700")
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(wire[6:], 1)
	use := func(body []byte) {
		t.Helper()
		if err := s.monsterContactGameplay(context.Background(), 1, actor, sinks[0], body); err != nil {
			t.Fatal(err)
		}
	}
	bad := append([]byte(nil), wire...)
	binary.LittleEndian.PutUint32(bad[6:], 2)
	use(bad[2:])
	bad = append([]byte(nil), wire...)
	binary.LittleEndian.PutUint32(bad[16:], 3000)
	use(bad[2:])
	if target.hp != 115000 {
		t.Fatal("forged owner or out-of-range damage was applied")
	}
	use(wire[2:])
	use(wire[2:]) // Same victim interval: no duplicate HP or visuals.
	if target.hp != 113679 || !target.nextMobContact.After(time.Now()) || settled != 0 {
		t.Fatalf("native nonlethal contact HP=%d settled=%d", target.hp, settled)
	}
	for _, sink := range sinks {
		if sink.count(protocol.SendMonsterHP) != 1 || sink.count(protocol.SendDamageMonster) != 0 || sink.count(protocol.SendMeleeAttack) != 0 {
			t.Fatal("contact did not publish exact HP, or echoed an unrelated damage packet")
		}
	}
	if sinks[0].count(protocol.SendMonsterContactDamage) != 0 || sinks[1].count(protocol.SendMonsterContactDamage) != 1 {
		t.Fatal("original mob damage did not reach only the observer")
	}
	for _, frame := range sinks[1].packets {
		if len(frame) >= 2 && binary.LittleEndian.Uint16(frame) == protocol.SendMonsterContactDamage &&
			hex.EncodeToString(frame) != "c90165420f00ff2905000052467d0000" {
			t.Fatalf("unexpected v148 OnDamagedByMob wire %x", frame)
		}
	}
	target.nextMobContact = time.Time{}
	target.statuses[combat.MobDazzle].OwnerGeneration++
	use(wire[2:])
	if target.hp != 113679 {
		t.Fatal("obsolete owner received another hit")
	}
	target.statuses[combat.MobDazzle].OwnerGeneration--
	source.controller, target.controller = 2, 2
	use(wire[2:])
	if target.hp != 112358 || sinks[1].count(protocol.SendMonsterContactDamage) != 2 {
		t.Fatal("movement controller overrode the valid native Dazzle reporter")
	}
	target.nextMobContact = time.Time{}
	target.hp = 500
	use(wire[2:])
	if settled != 1 || target.hp != 0 || sinks[0].count(protocol.SendKillMonster) != 1 || sinks[1].count(protocol.SendKillMonster) != 1 {
		t.Fatalf("lethal contact was not durably published: HP=%d settled=%d", target.hp, settled)
	}
}
