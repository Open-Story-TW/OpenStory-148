package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

var _ domain.CombatSettlementRepository = (*Store)(nil)

type combatWitness struct {
	CharacterID, AccountID                                int64
	SessionID                                             string
	Generation, Revision                                  int64
	WorldID, ChannelID, MapID, Level, Job, HP, MP, AP, SP int
	EXP                                                   int64
}

func combatWitnesses(members []domain.CharacterSession) []combatWitness {
	out := make([]combatWitness, 0, len(members))
	for _, m := range members {
		c := m.Character
		out = append(out, combatWitness{CharacterID: m.CharacterID, AccountID: m.AccountID, SessionID: m.SessionID, Generation: m.Generation, Revision: c.Revision, WorldID: m.Lease.WorldID, ChannelID: m.ChannelID, MapID: c.MapID, Level: c.Level, Job: c.Job, HP: c.HP, MP: c.MP, AP: c.AP, SP: c.SP, EXP: c.EXP})
	}
	return out
}
func combatProgressProjection(c domain.Character) domain.Character {
	c.Items = nil
	c.Equipment = nil
	c.Skills = nil
	c.SkillBuffs = nil
	// Cooldowns created by a committed skill must reach its live consumer.
	// Detach the slice; result serialization/publication must not alias input.
	c.SkillCooldowns = append([]domain.SkillCooldown(nil), c.SkillCooldowns...)
	c.InventorySlots = [5]int{}
	return c
}

func combatContributorIDs(targets []domain.CombatTarget) []int64 {
	ids := map[int64]bool{}
	for _, t := range targets {
		for _, c := range t.Contributions {
			ids[c.CharacterID] = true
		}
	}
	out := make([]int64, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func combatPartiesTx(ctx context.Context, tx *sql.Tx, world int, ids []int64) ([]domain.CombatPartyPin, error) {
	seen := map[int64]bool{}
	var out []domain.CombatPartyPin
	for _, id := range ids {
		if id <= 0 {
			return nil, domain.ErrInvalidInput
		}
		party, err := partyForCharacter(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if party == 0 || seen[party] {
			continue
		}
		p, err := readPartySnapshot(ctx, tx, party, world)
		if err != nil {
			return nil, err
		}
		pin := domain.CombatPartyPin{ID: p.ID, Revision: p.Revision}
		for _, m := range p.Members {
			pin.Members = append(pin.Members, m.CharacterID)
		}
		sort.Slice(pin.Members, func(i, j int) bool { return pin.Members[i] < pin.Members[j] })
		out = append(out, pin)
		seen[party] = true
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Store) CombatParties(ctx context.Context, lease domain.WorldLease, ids []int64) ([]domain.CombatPartyPin, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = guardLease(ctx, tx, lease); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1450036,$1)`, lease.WorldID); err != nil {
		return nil, err
	}
	return combatPartiesTx(ctx, tx, lease.WorldID, ids)
}

func (s *Store) CombatAttackOutcome(ctx context.Context, lease domain.WorldLease, op, hash string) (*domain.CombatAttackResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = guardLease(ctx, tx, lease); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, op); err != nil {
		return nil, err
	}
	var raw []byte
	var actual string
	err = tx.QueryRowContext(ctx, `SELECT payload_hash,result FROM asset_combat_attacks WHERE operation_id=$1 AND world_id=$2`, op, lease.WorldID).Scan(&actual, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if actual != hash {
		return nil, domain.ErrForbidden
	}
	var result domain.CombatAttackResult
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	result.Replayed = true
	return &result, nil
}

func carryCombatProgress(c *domain.Character, s domain.CharacterSession) error {
	v := s.Character
	if c.Revision != v.Revision {
		return domain.ErrStaleOwner
	}
	if c.Job != v.Job || c.BaseMaxHP != v.BaseMaxHP || c.BaseMaxMP != v.BaseMaxMP || c.VitalsModel != v.VitalsModel || c.VitalsOrigin != v.VitalsOrigin {
		return domain.ErrForbidden
	}
	if v.Level < 1 || v.Level > domain.MaxCharacterLevel() || v.EXP < 0 || v.EXP > domain.MaxExperience() || v.AP < 0 || v.AP > domain.MaxStat() || v.SP < 0 || v.SP > 65535 {
		return domain.ErrInvalidInput
	}
	for _, stat := range []int{v.STR, v.DEX, v.INT, v.LUK} {
		if stat < 4 || stat > domain.MaxStat() {
			return domain.ErrInvalidInput
		}
	}
	c.Level, c.EXP, c.STR, c.DEX, c.INT, c.LUK, c.AP, c.SP = v.Level, v.EXP, v.STR, v.DEX, v.INT, v.LUK, v.AP, v.SP
	if err := recomputeVitals(c); err != nil {
		return err
	}
	return carrySkillVitals(c, s)
}

func validateCombatCommand(q domain.CombatAttackRequest) error {
	if len(q.OperationID) < 16 || len(q.OperationID) > 200 || q.Family > domain.CombatSkill || len(q.Targets) > 15 || q.Family != domain.CombatSkill && len(q.Targets) > 6 || len(q.Members) < 1 {
		return domain.ErrInvalidInput
	}
	if q.Family == domain.CombatNormal && (q.SkillID != 0 || len(q.Targets) > 1) || q.Family == domain.CombatThreeSnails && (q.SkillID != 1000 || len(q.Targets) > 1) || q.Family == domain.CombatPhysicalSkill && q.SkillID != 1001004 && q.SkillID != 1001005 {
		return domain.ErrInvalidInput
	}
	if q.Family == domain.CombatThreeSnails {
		if q.LinesPerTarget != 1 || len(q.Wire) != 0 && len(q.Wire) != 33+30*len(q.Targets) {
			return domain.ErrInvalidInput
		}
		if len(q.Wire) > 0 {
			a, err := protocol.ReadBeginnerMagicAttack(protocol.NewReader(q.Wire))
			if err != nil || a.Tick != q.ClientTick || a.SkillID != q.SkillID || len(a.Targets) != len(q.Targets) {
				return domain.ErrInvalidInput
			}
			for i, target := range a.Targets {
				if target.ObjectID != q.Targets[i].ObjectID || len(q.Targets[i].Lines) != 1 || target.Lines[0].Raw != q.Targets[i].Lines[0].Raw {
					return domain.ErrInvalidInput
				}
			}
		}
	} else if q.Family == domain.CombatSkill {
		if q.LinesPerTarget < 1 || q.LinesPerTarget > 15 || q.Cost == nil || q.SkillID < 0 || q.SkillID > 99999999 {
			return domain.ErrInvalidInput
		}
		if err := validateMistEruptionWire(q); err != nil {
			return err
		}
	} else if q.LinesPerTarget != 0 || len(q.Wire) != 0 || q.Cost != nil {
		return domain.ErrInvalidInput
	}
	seen := map[int32]bool{}
	spawns := map[string]bool{}
	for _, t := range q.Targets {
		if q.Family == domain.CombatThreeSnails && len(t.Lines) != 1 || q.Family == domain.CombatSkill && len(t.Lines) != q.LinesPerTarget {
			return domain.ErrInvalidInput
		}
		if q.Family == domain.CombatNormal || q.Family == domain.CombatPhysicalSkill {
			// Old compatibility callers had no line audit. The live legacy
			// decoder now preserves its one nonnegative I32 raw line, including
			// damage capped by the formula or remaining HP. Keep that evidence
			// rather than rejecting every legacy kill or discarding the raw.
			if len(t.Lines) > 1 {
				return domain.ErrInvalidInput
			}
			if len(t.Lines) == 1 {
				line := t.Lines[0]
				if line.Raw > 0x7fffffff || line.Criticality != "" || line.Applied < 0 || line.Applied != t.Damage || int64(line.Applied) > line.Claimed() {
					return domain.ErrInvalidInput
				}
			}
		}
		if t.ObjectID <= 0 || t.MobID <= 0 || len(t.SpawnOperationID) < 16 || len(t.SpawnOperationID) > 200 || t.BeforeHP < 1 || t.Damage < 0 || int64(t.Damage) > t.BeforeHP || t.Defeated != (int64(t.Damage) == t.BeforeHP) || seen[t.ObjectID] || spawns[t.SpawnOperationID] {
			return domain.ErrInvalidInput
		}
		seen[t.ObjectID] = true
		spawns[t.SpawnOperationID] = true
	}
	return nil
}

// ApplyCombatAttack has one commit for MP, every recipient's progress, all
// quest kills/rosters, and one pinned loot job per defeated spawn.
func (s *Store) ApplyCombatAttack(ctx context.Context, q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
	q.Targets = append([]domain.CombatTarget(nil), q.Targets...)
	sort.Slice(q.Targets, func(i, j int) bool { return q.Targets[i].ObjectID < q.Targets[j].ObjectID })
	result := domain.CombatAttackResult{OperationID: q.OperationID}
	if err := validateCombatCommand(q); err != nil {
		return result, err
	}
	hash, err := domain.CombatManifestHash(q)
	if err != nil {
		return result, err
	}
	result.ManifestHash = hash
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = guardBuddyOwner(ctx, tx, q.Session); err != nil {
		return result, err
	}
	// Serialize the command identity before receipt lookup, including commands
	// with no target. Character locks below always use ascending IDs.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, q.OperationID); err != nil {
		return result, err
	}
	var oldOp, oldHash string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT operation_id,payload_hash,result FROM asset_combat_attacks WHERE operation_id=$1 OR(actor_id=$2 AND session_id=$3 AND generation=$4 AND client_tick=$5)`, q.OperationID, q.Session.CharacterID, q.Session.SessionID, q.Session.Generation, int64(q.ClientTick)).Scan(&oldOp, &oldHash, &raw)
	if err == nil {
		if oldOp != q.OperationID || oldHash != hash {
			return result, domain.ErrForbidden
		}
		if err = json.Unmarshal(raw, &result); err != nil {
			return result, err
		}
		result.Replayed = true
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1450036,$1)`, q.Session.Lease.WorldID); err != nil {
		return result, err
	}
	actual, err := combatPartiesTx(ctx, tx, q.Session.Lease.WorldID, combatContributorIDs(q.Targets))
	if err != nil {
		return result, err
	}
	a, b := q, q
	a.Members = nil
	a.Targets = nil
	a.Parties = actual
	b.Members = nil
	b.Targets = nil
	ah, _ := domain.CombatManifestHash(a)
	bh, _ := domain.CombatManifestHash(b)
	if ah != bh {
		return result, domain.ErrStaleOwner
	}
	members := append([]domain.CharacterSession(nil), q.Members...)
	sort.Slice(members, func(i, j int) bool { return members[i].CharacterID < members[j].CharacterID })
	chars := map[int64]domain.Character{}
	sessions := map[int64]domain.CharacterSession{}
	for _, m := range members {
		if m.Lease.WorldID != q.Session.Lease.WorldID || m.Lease.InstanceID != q.Session.Lease.InstanceID || m.Lease.Epoch != q.Session.Lease.Epoch || m.ChannelID != q.Session.ChannelID || m.Character.MapID != q.Session.Character.MapID || chars[m.CharacterID].ID != 0 {
			return result, domain.ErrInvalidInput
		}
		c, e := lockInventoryCharacter(ctx, tx, m)
		if e != nil {
			return result, e
		}
		if c.MapID != m.Character.MapID {
			return result, domain.ErrStaleOwner
		}
		if e = carryCombatProgress(&c, m); e != nil {
			return result, e
		}
		chars[c.ID] = c
		sessions[c.ID] = m
	}
	actor, ok := chars[q.Session.CharacterID]
	if !ok || actor.HP <= 0 || sessions[actor.ID].SessionID != q.Session.SessionID || sessions[actor.ID].Generation != q.Session.Generation {
		return result, domain.ErrForbidden
	}
	if len(q.MesoDrops) > 0 {
		// Meso Explosion retires its ground mesos with the attack, so a rejected
		// settlement leaves them on the field.
		ids := append([]int32(nil), q.MesoDrops...)
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		if q.Family != domain.CombatSkill || len(ids) > 32 {
			return result, domain.ErrInvalidInput
		}
		for i := 1; i < len(ids); i++ {
			if ids[i] == ids[i-1] {
				return result, domain.ErrInvalidInput
			}
		}
		if err = lockItemRoom(ctx, tx, actor.WorldID, q.Session.ChannelID, actor.MapID); err != nil {
			return result, err
		}
		if _, err = consumeMesoDropsTx(ctx, tx, actor, q.Session.ChannelID, "meso-"+q.OperationID, q.SkillID, ids); err != nil {
			return result, err
		}
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return result, err
	}
	mp, err := combatAttackCost(actor, q, now)
	if err != nil {
		return result, err
	}
	if actor.MP < mp {
		return result, domain.ErrInsufficient
	}
	actor.MP -= mp
	if q.Family == domain.CombatSkill {
		if result.Ammo, err = payCombatSkillCost(ctx, tx, &actor, q, now); err != nil {
			return result, err
		}
	}
	chars[actor.ID] = actor
	changed := map[int64]bool{actor.ID: true}
	frozenMembers := make([]domain.CharacterSession, 0, len(members))
	for _, m := range members {
		m.Character = chars[m.CharacterID]
		frozenMembers = append(frozenMembers, m)
	}
	for _, target := range q.Targets {
		rules, e := content.CombatTargetExperienceMetadata(os.Getenv("MAPLEGO_WZ_ROOT"), actor.MapID, target)
		if e != nil {
			return result, e
		}
		if e = validateCombatTarget(actor, q, target, rules, now); e != nil {
			return result, e
		}
		var settled bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM asset_combat_rewards WHERE operation_id=$1)`, target.SpawnOperationID).Scan(&settled); e != nil {
			return result, e
		}
		if settled {
			return result, domain.ErrAlreadyExists
		}
		if !target.Defeated {
			continue
		}
		frozen := domain.CombatSpawnResult{SpawnOperationID: target.SpawnOperationID, MobID: target.MobID}
		frozen.Grants, e = content.CombatExperienceGrants(rules, actor.WorldID, q.Session.ChannelID, actor.MapID, target.Contributions, frozenMembers, actual)
		if e != nil {
			return result, e
		}
		for i := range frozen.Grants {
			grant := &frozen.Grants[i]
			c := chars[grant.CharacterID]
			grant.Experience = bonusExperience(domain.RateExperience(grant.Experience), skills.Bonuses(c, now).EXP)
			if e = applyQuestExperience(&c, grant.Experience, target.SpawnOperationID); e != nil {
				return result, e
			}
			if grant.QuestKill {
				if e = recordQuestKill(ctx, tx, &c, target.SpawnOperationID, target.MobID); e != nil {
					return result, e
				}
			}
			chars[c.ID] = c
			changed[c.ID] = true
		}
		result.Spawns = append(result.Spawns, frozen)
	}
	for _, m := range members {
		if !changed[m.CharacterID] {
			continue
		}
		c := chars[m.CharacterID]
		if _, err = tx.ExecContext(ctx, `UPDATE game_characters SET level=$1,exp=$2,str=$3,dex=$4,int_stat=$5,luk=$6,ap=$7,sp=$8 WHERE id=$9`, c.Level, c.EXP, c.STR, c.DEX, c.INT, c.LUK, c.AP, c.SP, c.ID); err != nil {
			return result, err
		}
		if err = commitInventoryState(ctx, tx, &c); err != nil {
			return result, err
		}
		chars[c.ID] = c
		result.Characters = append(result.Characters, combatProgressProjection(c))
	}
	for _, spawn := range result.Spawns {
		var target domain.CombatTarget
		for _, t := range q.Targets {
			if t.SpawnOperationID == spawn.SpawnOperationID {
				target = t
				break
			}
		}
		rules, e := content.CombatTargetExperienceMetadata(os.Getenv("MAPLEGO_WZ_ROOT"), actor.MapID, target)
		if e != nil {
			return result, e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO asset_combat_rewards(operation_id,character_id,experience,character_revision) VALUES($1,$2,$3,$4)`, spawn.SpawnOperationID, actor.ID, rules.Experience, chars[actor.ID].Revision); err != nil {
			return result, err
		}
		raw, e = json.Marshal(struct {
			Target    domain.CombatTarget
			Parties   []domain.CombatPartyPin
			Grants    []domain.CombatGrant
			Witnesses []combatWitness
			Rules     content.CombatExperienceRules
		}{target, actual, spawn.Grants, combatWitnesses(frozenMembers), rules})
		if e != nil {
			return result, e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO asset_combat_settlements(spawn_operation_id,attack_operation_id,mob_id,manifest) VALUES($1,$2,$3,$4)`, spawn.SpawnOperationID, q.OperationID, target.MobID, raw); err != nil {
			return result, err
		}
		for _, g := range spawn.Grants {
			if _, err = tx.ExecContext(ctx, `INSERT INTO asset_combat_grants(spawn_operation_id,character_id,experience,base_experience,party_experience,character_revision) VALUES($1,$2,$3,$4,$5,$6)`, spawn.SpawnOperationID, g.CharacterID, g.Experience, g.BaseEXP, g.PartyEXP, chars[g.CharacterID].Revision); err != nil {
				return result, err
			}
		}
		if err = enqueueCombatLootTx(ctx, tx, q.Session, chars[actor.ID], spawn.SpawnOperationID, target.MobID, target.X, target.Y); err != nil {
			return result, err
		}
	}
	if err = insertCombatCompatibilityReceipt(ctx, tx, q, hash); err != nil {
		return result, err
	}
	raw, err = json.Marshal(result)
	if err != nil {
		return result, err
	}
	manifest, err := json.Marshal(struct {
		Family          domain.CombatFamily
		SkillID, MPCost int
		Tick            uint32
		Parties         []domain.CombatPartyPin
		Witnesses       []combatWitness
		Targets         []domain.CombatTarget
		LinesPerTarget  int
		Wire            []byte
	}{q.Family, q.SkillID, mp, q.ClientTick, actual, combatWitnesses(frozenMembers), q.Targets, q.LinesPerTarget, q.Wire})
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_combat_attacks(operation_id,actor_id,world_id,session_id,generation,client_tick,family,payload_hash,result,manifest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, q.OperationID, actor.ID, actor.WorldID, q.Session.SessionID, q.Session.Generation, int64(q.ClientTick), int(q.Family), hash, raw, manifest); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	for _, c := range result.Characters {
		s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	}
	return result, nil
}

// bonusExperience applies the learned passive EXP percent (skills.Bonus.EXP,
// e.g. Zero's Spirit blessing) to one combat grant.
func bonusExperience(exp int64, percent int) int64 {
	if exp <= 0 || percent <= 0 {
		return exp
	}
	// Dividing first keeps the product in range for a 64-bit grant.
	bonus := exp/100*int64(percent) + exp%100*int64(percent)/100
	return domain.AddExperience(exp, bonus)
}
