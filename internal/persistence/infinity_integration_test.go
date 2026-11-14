package persistence

import (
	"errors"
	"fmt"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"os"
	"testing"
	"time"
)

func TestInfinityPGCastTickReplayCancelAndExpiry(t *testing.T) {
	originalWZ := os.Getenv("MAPLEGO_WZ_ROOT")
	usePrivateSchema(t, "infinity")
	t.Setenv("MAPLEGO_WZ_ROOT", originalWZ)
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	if _, e := s.db.ExecContext(ctx, `UPDATE game_characters SET job=212,level=150,base_max_hp=1000,max_hp=1000,hp=100,base_max_mp=1000,max_mp=1000,mp=300 WHERE id=$1`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.ExecContext(ctx, `INSERT INTO game_skills(character_id,skill_id,level,master_level) VALUES($1,2121004,30,30)`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	var e error
	a.Character, e = readCharacterSQL(ctx, s.db, a.CharacterID)
	if e != nil {
		t.Fatal(e)
	}
	q := domain.SkillCastRequest{Session: a, SkillID: 2121004, ClientLevel: 30, OperationID: fmt.Sprintf("infinity-cast-operation-%d", a.CharacterID)}
	r, e := s.CastSkill(ctx, q)
	if e != nil || r.Character.HP != 200 || r.Character.MP != 320 || len(r.Character.SkillBuffs) != 2 {
		t.Fatalf("cast %+v %v", r, e)
	}
	var original domain.SkillBuff
	for _, b := range r.Character.SkillBuffs {
		if b.Stat == "INFINITY" {
			original = b
		}
	}
	if original.Value != 1 || original.NextTick.IsZero() || len(r.Character.SkillCooldowns) != 1 {
		t.Fatalf("timer/cooldown %+v", r)
	}
	if replay, e := s.CastSkill(ctx, q); e != nil || !replay.Replayed || replay.Character.HP != 200 || replay.Character.MP != 320 {
		t.Fatalf("replay %+v %v", replay, e)
	}
	a.Character = r.Character
	if tick, e := s.TickSkillBuffs(ctx, a); e != nil || tick.Changed || tick.Character.HP != 200 {
		t.Fatalf("early %+v %v", tick, e)
	}
	if _, e = s.db.ExecContext(ctx, `UPDATE game_skill_buffs SET next_tick=clock_timestamp()-interval '20 seconds' WHERE character_id=$1 AND stat='INFINITY'`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	tick, e := s.TickSkillBuffs(ctx, a)
	if e != nil || !tick.Changed || tick.Character.HP != 300 || tick.Character.MP != 420 || len(tick.Updated) != 1 || tick.Updated[0].Value != 11 || !tick.Updated[0].ExpiresAt.Equal(original.ExpiresAt) {
		t.Fatalf("one delayed callback %+v %v", tick, e)
	}
	if _, e = s.TickSkillBuffs(ctx, a); !errors.Is(e, domain.ErrStaleOwner) {
		t.Fatalf("old revision tick: %v", e)
	}
	a.Character = tick.Character
	if twice, e := s.TickSkillBuffs(ctx, a); e != nil || twice.Changed || twice.Character.HP != 300 || twice.Character.MP != 420 {
		t.Fatalf("duplicate %+v %v", twice, e)
	}
	loaded, e := readCharacterSQL(ctx, s.db, a.CharacterID)
	if e != nil {
		t.Fatal(e)
	}
	if loaded.HP != 300 || loaded.MP != 420 {
		t.Fatalf("not durable %+v", loaded)
	}
	for _, b := range loaded.SkillBuffs {
		if b.Stat == "INFINITY" && (b.Value != 11 || b.NextTick.IsZero()) {
			t.Fatalf("lost timer %+v", b)
		}
	}

	// Expired Hyper Body must not inflate the next percentage recovery.
	if _, e = s.db.ExecContext(ctx, `INSERT INTO game_skill_buffs(character_id,skill_id,level,stat,value,expires_at,counter) VALUES($1,1301007,20,'MAXHP',60,clock_timestamp()-interval '1 second',0),($1,1301007,20,'MAXMP',60,clock_timestamp()-interval '1 second',0)`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.ExecContext(ctx, `UPDATE game_characters SET max_hp=1600,max_mp=1600 WHERE id=$1`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.ExecContext(ctx, `UPDATE game_skill_buffs SET next_tick=clock_timestamp()-interval '1 second' WHERE character_id=$1 AND stat='INFINITY'`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	fresh, e := s.TickSkillBuffs(ctx, a)
	if e != nil || fresh.Character.MaxHP != 1000 || fresh.Character.MaxMP != 1000 || fresh.Character.HP != 400 || fresh.Character.MP != 520 || len(fresh.Expired) != 2 {
		t.Fatalf("stale cap recovery %+v %v", fresh, e)
	}
	a.Character = fresh.Character
	cancel, e := s.CancelSkill(ctx, a, 2121004)
	if e != nil || len(cancel.Character.SkillBuffs) != 0 || len(cancel.Character.SkillCooldowns) != 1 || cancel.Character.HP != 400 || cancel.Character.MP != 520 || len(cancel.Updated) != 0 {
		t.Fatalf("manual cancel ticked/refunded %+v %v", cancel, e)
	}
	// A fresh cast after independently expiring its cooldown verifies natural
	// expiry, including a due timer at the deadline, never performs one last heal.
	a.Character = cancel.Character
	if _, e = s.db.ExecContext(ctx, `DELETE FROM game_skill_cooldowns WHERE character_id=$1`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	q.Session = a
	q.OperationID += "-again"
	r, e = s.CastSkill(ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	a.Character = r.Character
	if _, e = s.db.ExecContext(ctx, `UPDATE game_skill_buffs SET expires_at=clock_timestamp()-interval '1 second',next_tick=clock_timestamp()-interval '2 seconds' WHERE character_id=$1`, a.CharacterID); e != nil {
		t.Fatal(e)
	}
	expired, e := s.TickSkillBuffs(ctx, a)
	if e != nil || len(expired.Character.SkillBuffs) != 0 || len(expired.Expired) != 2 || expired.Character.HP != r.Character.HP || expired.Character.MP != r.Character.MP || len(expired.Updated) != 0 {
		t.Fatalf("expired %+v %v", expired, e)
	}
}

func TestInfinityRowsOnlyScheduleReviewedStat(t *testing.T) {
	now := time.Now()
	rows, ok := skillBuffRows(2121004, 30, &skills.Buff{Duration: 40 * time.Second, Tick: 4 * time.Second, Stats: []skills.StatValue{{Stat: "INFINITY", Value: 1}, {Stat: "STANCE", Value: 70}}}, now)
	if !ok || rows[0].NextTick != now.Add(4*time.Second) || !rows[1].NextTick.IsZero() {
		t.Fatalf("rows %+v %v", rows, ok)
	}
	if _, ok = skillBuffRows(1001003, 1, &skills.Buff{Duration: 40 * time.Second, Tick: 4 * time.Second, Stats: []skills.StatValue{{Stat: "INFINITY", Value: 1}}}, now); ok {
		t.Fatal("unreviewed source acquired timer")
	}
}
