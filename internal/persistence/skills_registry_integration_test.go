package persistence

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

func TestSpiritLinkCanonicalRowsPublishLearnedSource(t *testing.T) {
	now := time.Unix(1000, 0)
	buff := &skills.Buff{Duration: time.Minute, Stats: []skills.StatValue{
		{Stat: "SUMMON", Value: 1},
		{Stat: "SPIRIT_LINK", Value: 1},
		{Stat: "ELEMENTAL_STATUS_R", Value: 15},
		{Stat: "DAMAGE_BUFF", Value: 15},
	}}
	rows, ok := skillBuffRows(3111005, 20, buff, now)
	if !ok || len(rows) != 4 || rows[1].Stat != "SPIRIT_SURGE" {
		t.Fatalf("canonical rows: %+v", rows)
	}
	packet := protocol.SkillBuffsAmong(rows, rows, now)
	r := protocol.NewReader(packet)
	if r.U16() != 0x2d {
		t.Fatalf("no local buff: %x", packet)
	}
	for range 8 {
		r.U32()
	}
	for i, want := range []int32{3111005, 3111005, 3111005, 3120006} {
		r.I32() // value
		if source := r.I32(); source != want || r.I32() != 60000 {
			t.Fatalf("triplet %d source=%d, want %d: %x", i, source, want, packet)
		}
	}
	if r.Err() != nil {
		t.Fatal(r.Err())
	}
}

// The registry-driven CastSkill must keep every pre-registry skill behaviour.
// Each legacy test runs against its own private schema.
func TestRegistryCastKeepsLegacySkills(t *testing.T) {
	for name, run := range map[string]func(*testing.T){
		"recovery-nimble":   TestSkillAllocationCastCooldownAndRecovery,
		"beginner-pool":     TestSkillBeginnerPointPoolAndClientLevelValidation,
		"iron-body":         TestIronBodySPCostPersistentBuffReplayReplacementAndExpiry,
		"three-snails-cost": TestThreeSnailsCostUsesAdventurerAncestryAndLiveSkill,
		"mp-expansion":      TestMPExpansionAtomicAllocationReplayRollbackAndEquipment,
		"vitals-clamp":      TestBaseVitalsOfflineTimedPassiveClampAndAmbiguousPreservation,
	} {
		t.Run(name, func(t *testing.T) {
			usePrivateSchema(t, "s0a")
			run(t)
		})
	}
}

// One skill owns several stat rows; a later effect takes over a shared stat,
// and MAXHP/MAXMP rows drive the derived maxima until they expire.
func TestMultiStatBuffRowsReplaceStatsAndDriveMaxima(t *testing.T) {
	usePrivateSchema(t, "s0a")
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET job=130,level=30,base_max_hp=200,max_hp=200,hp=200,base_max_mp=100,max_mp=100,mp=100 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	c, err := readCharacterSQL(ctx, s.db, a.CharacterID)
	if err != nil || c.VitalsModel != 1 {
		t.Fatalf("fixture %+v %v", c, err)
	}
	now := time.Now()
	plan := skills.CastPlan{Self: &skills.Buff{Duration: time.Minute, Stats: []skills.StatValue{{Stat: "MAXMP", Value: 60}, {Stat: "MAXHP", Value: 60}, {Stat: "WDEF", Value: 7}}}}
	rows, ok := skillBuffRows(1301007, 20, plan.Self, now)
	if !ok || len(rows) != 3 {
		t.Fatalf("rows %+v", rows)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := domain.SkillResult{}
	if err = replaceSkillBuffs(ctx, tx, &c, &r, rows); err != nil || c.MaxHP != 320 || c.MaxMP != 160 || c.HP != 200 || len(r.Expired) != 0 {
		t.Fatalf("hyper body maxima %d/%d hp %d expired %v %v", c.MaxHP, c.MaxMP, c.HP, r.Expired, err)
	}
	c.HP = 300
	iron, _ := skillBuffRows(1001003, 1, &skills.Buff{Duration: time.Minute, Stats: []skills.StatValue{{Stat: "WDEF", Value: 10}}}, now)
	r = domain.SkillResult{}
	if err = replaceSkillBuffs(ctx, tx, &c, &r, iron); err != nil || len(r.Expired) != 1 || r.Expired[0].ID != 1301007 || r.Expired[0].Stat != "WDEF" || len(c.SkillBuffs) != 3 {
		t.Fatalf("shared stat takeover %+v %+v %v", r.Expired, c.SkillBuffs, err)
	}
	if err = commitInventoryState(ctx, tx, &c); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	loaded, err := readCharacterSQL(ctx, s.db, a.CharacterID)
	if err != nil || len(loaded.SkillBuffs) != 3 || loaded.MaxHP != 320 || loaded.HP != 300 {
		t.Fatalf("reload %+v %v", loaded, err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE game_skill_buffs SET expires_at=clock_timestamp()-interval '1 second' WHERE character_id=$1 AND skill_id=1301007`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	a.Character = loaded
	out, err := s.TickSkillBuffs(ctx, a)
	if err != nil || len(out.Expired) != 2 || out.Character.MaxHP != 200 || out.Character.MaxMP != 100 || out.Character.HP != 200 || len(out.Character.SkillBuffs) != 1 {
		t.Fatalf("expiry clamp %+v %+v %v", out.Expired, out.Character, err)
	}
	if _, ok = skillBuffRows(1, 1, &skills.Buff{Duration: time.Minute, Stats: []skills.StatValue{{Stat: "NOT_A_STAT", Value: 1}}}, now); ok {
		t.Fatal("unknown stat accepted")
	}
	if _, ok = skillBuffRows(1, 1, &skills.Buff{Duration: time.Minute, Tick: time.Second, Stats: []skills.StatValue{{Stat: "WDEF", Value: 1}}}, now); ok {
		t.Fatal("periodic effect without a reviewed tick accepted")
	}
}
