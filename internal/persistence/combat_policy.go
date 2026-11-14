package persistence

import (
	"context"
	"database/sql"
	"os"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

// Compatibility APIs have no live multi-recipient ledger. They are never a
// party fallback: live handlers must call ApplyCombatAttack with full context.
func rejectPartyCombatCompatibility(ctx context.Context, tx *sql.Tx, id int64) error {
	party, err := partyForCharacter(ctx, tx, id)
	if err != nil {
		return err
	}
	if party != 0 {
		return domain.ErrForbidden
	}
	return nil
}

func combatAttackCost(c domain.Character, q domain.CombatAttackRequest, now time.Time) (int, error) {
	if c.HP <= 0 {
		return 0, domain.ErrForbidden
	}
	if q.Family == domain.CombatNormal || q.Family == domain.CombatPhysicalSkill || q.Family == domain.CombatThreeSnails {
		if _, err := content.PreparePotentialPhysicalMaximum(os.Getenv("MAPLEGO_WZ_ROOT"), c, now); err != nil {
			return 0, err
		}
	}
	switch q.Family {
	case domain.CombatNormal:
		if c.Job != 0 && c.Job != 100 && c.Job != 130 {
			return 0, domain.ErrForbidden
		}
		return 0, nil
	case domain.CombatThreeSnails:
		state := learned(c, 1000)
		if state.Level < 1 || !state.ExpiresAt.IsZero() && !state.ExpiresAt.After(now) {
			return 0, domain.ErrForbidden
		}
		skill, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), 1000)
		if err != nil || skill.Disabled {
			return 0, domain.ErrInvalidInput
		}
		if !skill.CanLearnBy(c) {
			return 0, domain.ErrForbidden
		}
		values, err := skill.Values(state.Level)
		if err != nil || values["mpCon"] < 0 || values["fixdamage"] <= 0 {
			return 0, domain.ErrInvalidInput
		}
		resource, err := skills.ResolveResourceCost(c, q.SkillID, values["mpCon"], 0, now)
		return resource.MP, err
	case domain.CombatPhysicalSkill:
		profile, err := content.PhysicalAttackProfileFor(os.Getenv("MAPLEGO_WZ_ROOT"), c, q.SkillID, now)
		if err != nil {
			return 0, domain.ErrForbidden
		}
		if len(q.Targets) > profile.MaxTargets {
			return 0, domain.ErrInvalidInput
		}
		for _, cd := range c.SkillCooldowns {
			if cd.ID == q.SkillID && cd.ExpiresAt.After(now) {
				return 0, domain.ErrAlreadyExists
			}
		}
		resource, err := skills.ResolveResourceCost(c, q.SkillID, profile.MP, 0, now)
		return resource.MP, err
	case domain.CombatSkill:
		return genericCombatCost(c, q, now)
	}
	return 0, domain.ErrInvalidInput
}

// genericCombatCost bounds the world-planned cost by the learned WZ level:
// passives may only lower MP/HP cost, never raise it or skip the cooldown.
func genericCombatCost(c domain.Character, q domain.CombatAttackRequest, now time.Time) (int, error) {
	cost := q.Cost
	if cost == nil || cost.MP < 0 || cost.HP < 0 || cost.Ammo < 0 || cost.Ammo > 30 || cost.CooldownMS < 0 || cost.CooldownMS > 3600000 {
		return 0, domain.ErrInvalidInput
	}
	if q.SkillID == 0 {
		if cost.MP != 0 || cost.HP != 0 || cost.Level != 0 || cost.CooldownMS != 0 {
			return 0, domain.ErrInvalidInput
		}
		resource, ongoing, err := skills.AttackResourceCost(c, 0, 0, cost.Opcode, now)
		if err != nil {
			return 0, err
		}
		if cost.Ongoing != ongoing || cost.MP != resource.MP || cost.CooldownMS != int(resource.Cooldown.Milliseconds()) {
			return 0, domain.ErrForbidden
		}
		return resource.MP, nil
	}
	source := q.SkillID
	var spec *combat.AttackSpec
	if h, ok := skills.Lookup(q.SkillID); ok {
		spec = h.Attack
		source = combat.LevelSource(q.SkillID, spec)
		if source == q.SkillID && h.LevelFrom != 0 {
			source = h.LevelFrom
		}
	}
	state := learned(c, source)
	if state.Level < 1 || !state.ExpiresAt.IsZero() && !state.ExpiresAt.After(now) {
		return 0, domain.ErrForbidden
	}
	if cost.Summon != 0 {
		// Summon attacks cost nothing; their level came from the cast (linked
		// summons use the parent's) and their target bound is the summon's
		// own attack info, validated by the world against the registered spec.
		if spec == nil || spec.Summon == nil || !spec.Allows(combat.Summoned) || cost.Opcode != 0 && cost.Opcode != protocol.RecvSummonAttack || cost.Ongoing || cost.MP != 0 || cost.HP != 0 || cost.Ammo != 0 || cost.CooldownMS != 0 || cost.Level < 1 || cost.Level > 255 || len(q.Targets) > 15 || q.LinesPerTarget != 1 {
			return 0, domain.ErrInvalidInput
		}
		return 0, nil
	}
	skill, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), q.SkillID)
	if err != nil {
		return 0, domain.ErrInvalidInput
	}
	// Linked skills cap at their own MaxLevel; COMBAT_ORDERS raises both up
	// to TrueMax (Java getTotalSkillLevel), exactly as the world planned.
	level := skills.LinkedLevel(c, source, q.SkillID, now)
	if level != cost.Level {
		return 0, domain.ErrForbidden
	}
	e, err := combat.FromSkill(skill, level)
	ratio := max(e.X, e.Y, e.Z, 0)
	if err != nil || cost.HP > e.HPCon+c.HP*min(ratio, 100)/100 || cost.HPGain < 0 || cost.MPGain < 0 {
		return 0, domain.ErrInvalidInput
	}
	resource, ongoing, err := skills.AttackResourceCost(c, q.SkillID, level, cost.Opcode, now)
	if err != nil {
		return 0, err
	}
	if cost.MP != resource.MP || cost.CooldownMS != int(resource.Cooldown.Milliseconds()) || cost.Ongoing != ongoing {
		return 0, domain.ErrForbidden
	}
	var dealt int64
	for _, t := range q.Targets {
		dealt += int64(t.Damage)
	}
	if int64(cost.HPGain) > dealt || cost.MPGain > c.MaxMP || cost.Meso < 0 || cost.Meso > e.MoneyCon {
		return 0, domain.ErrInvalidInput
	}
	if cost.Ammo > 0 && !combat.AmmoItemMatches(cost.AmmoItemID) && (cost.AmmoItemID != e.ItemCon || cost.Ammo > max(1, e.ItemConNo)) {
		return 0, domain.ErrInvalidInput
	}
	if len(q.Targets) > e.MobCount || q.LinesPerTarget > 2*max(e.AttackCount, e.BulletCount) {
		return 0, domain.ErrInvalidInput
	}
	if cost.Ongoing {
		if cost.CooldownMS != 0 {
			return 0, domain.ErrInvalidInput
		}
		return cost.MP, nil
	}
	for _, cd := range c.SkillCooldowns {
		if cd.ID == q.SkillID && cd.ExpiresAt.After(now) {
			return 0, domain.ErrAlreadyExists
		}
	}
	return cost.MP, nil
}

func validateCombatTarget(c domain.Character, q domain.CombatAttackRequest, t domain.CombatTarget, r content.CombatExperienceRules, now time.Time) error {
	if t.BeforeHP > r.MaximumHP {
		return domain.ErrInvalidInput
	}
	catalog, err := content.DefaultCatalog()
	if err != nil {
		return err
	}
	mob, err := catalog.Mob(t.MobID)
	if err != nil {
		return err
	}
	maximum := 0
	switch q.Family {
	case domain.CombatNormal:
		maximum, err = content.NormalAttackMaximum(os.Getenv("MAPLEGO_WZ_ROOT"), c, mob, now)
	case domain.CombatPhysicalSkill:
		p, e := content.PhysicalAttackProfileFor(os.Getenv("MAPLEGO_WZ_ROOT"), c, q.SkillID, now)
		if e != nil {
			return domain.ErrForbidden
		}
		maximum, err = p.MaximumDamage(mob)
	case domain.CombatThreeSnails:
		if mob.Invincible || mob.OnlyNormalAttack || mob.HasFixedDamage || mob.FixedDamage > 0 {
			return domain.ErrForbidden
		}
		skill, e := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), 1000)
		if e != nil {
			return domain.ErrInvalidInput
		}
		values, e := skill.Values(learned(c, 1000).Level)
		if e != nil {
			return domain.ErrInvalidInput
		}
		maximum = values["fixdamage"]
	case domain.CombatSkill:
		// Formula ceilings ran in the world with full effective stats; the
		// durable bound is the per-line display cap and monster policy.
		if mob.Invincible || mob.OnlyNormalAttack && q.SkillID != 0 || len(t.Lines) != q.LinesPerTarget {
			return domain.ErrForbidden
		}
		sum := 0
		for _, l := range t.Lines {
			if l.Applied < 0 || int64(l.Applied) > l.Claimed() || l.Applied > combat.MaxLineDamage() {
				return domain.ErrInvalidInput
			}
			if combat.ReviewedWireCriticality(combat.Melee, q.SkillID, l.Raw) != "" {
				if q.Cost == nil {
					return domain.ErrInvalidInput
				}
				expected := combat.ReviewedWireCriticality(combat.KindForOpcode(q.Cost.Opcode), q.SkillID, l.Raw)
				if l.Criticality != "" && l.Criticality != expected {
					return domain.ErrInvalidInput
				}
				// The world owns transient target statuses and the precise
				// formula bound. Keep the existing durable display-cap policy;
				// template PDR alone cannot reproduce that transient context.
				// Empty metadata is a legacy receipt, not proof of noncritical.
			} else if l.Criticality != "" {
				return domain.ErrInvalidInput
			}
			sum += l.Applied
		}
		if sum != t.Damage {
			return domain.ErrInvalidInput
		}
		maximum = t.Damage
	default:
		return domain.ErrInvalidInput
	}
	if err != nil || t.Damage > maximum {
		return domain.ErrInvalidInput
	}
	if q.Family == domain.CombatThreeSnails {
		if len(t.Lines) != 1 || t.Lines[0].Raw&0x80000000 != 0 || int64(t.Lines[0].Raw) > int64(maximum) || t.Lines[0].Applied != t.Damage || t.Lines[0].Applied > int(t.Lines[0].Raw) {
			return domain.ErrInvalidInput
		}
	}
	var total, actorDamage int64
	type identity struct {
		id, gen int64
		sid     string
	}
	seen := map[identity]bool{}
	for _, d := range t.Contributions {
		key := identity{d.CharacterID, d.Generation, d.SessionID}
		if d.CharacterID <= 0 || d.Generation < 1 || d.SessionID == "" || d.Damage <= 0 || d.Damage > r.MaximumHP || seen[key] {
			return domain.ErrInvalidInput
		}
		seen[key] = true
		if d.Damage > r.MaximumHP-total {
			return domain.ErrInvalidInput
		}
		total += d.Damage
		if d.CharacterID == q.Session.CharacterID && d.Generation == q.Session.Generation && d.SessionID == q.Session.SessionID {
			actorDamage += d.Damage
		}
	}
	if total != r.MaximumHP-t.BeforeHP+int64(t.Damage) || actorDamage < int64(t.Damage) {
		return domain.ErrInvalidInput
	}
	return nil
}

func insertCombatCompatibilityReceipt(ctx context.Context, tx *sql.Tx, q domain.CombatAttackRequest, hash string) error {
	var prior bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM asset_skill_attacks WHERE character_id=$1 AND session_id=$2 AND generation=$3 AND client_tick=$4 UNION ALL SELECT 1 FROM asset_physical_attacks WHERE character_id=$1 AND session_id=$2 AND generation=$3 AND client_tick=$4)`, q.Session.CharacterID, q.Session.SessionID, q.Session.Generation, int64(q.ClientTick)).Scan(&prior); err != nil {
		return err
	}
	if prior {
		return domain.ErrForbidden
	}
	switch q.Family {
	case domain.CombatPhysicalSkill:
		_, err := tx.ExecContext(ctx, `INSERT INTO asset_physical_attacks(character_id,operation_id,session_id,generation,client_tick,skill_id,payload_hash) VALUES($1,$2,$3,$4,$5,$6,$7)`, q.Session.CharacterID, q.OperationID, q.Session.SessionID, q.Session.Generation, int64(q.ClientTick), q.SkillID, hash)
		return err
	case domain.CombatThreeSnails:
		var t domain.CombatTarget
		if len(q.Targets) > 0 {
			t = q.Targets[0]
		}
		var exp int64
		if t.Defeated {
			c, err := content.DefaultCatalog()
			if err != nil {
				return err
			}
			mob, err := c.Mob(t.MobID)
			if err != nil {
				return err
			}
			exp = mob.EXP
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO asset_skill_attacks(character_id,operation_id,spawn_operation_id,session_id,generation,client_tick,skill_id,target_object_id,target_mob_id,damage,defeated,experience) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, q.Session.CharacterID, q.OperationID, t.SpawnOperationID, q.Session.SessionID, q.Session.Generation, int64(q.ClientTick), 1000, t.ObjectID, t.MobID, t.Damage, t.Defeated, exp)
		return err
	}
	return nil
}

// payCombatSkillCost takes HP, projectiles and the cooldown in the attack
// transaction. Exhausted stacks are removed.
func payCombatSkillCost(ctx context.Context, tx *sql.Tx, c *domain.Character, q domain.CombatAttackRequest, now time.Time) (*domain.CombatAmmoResult, error) {
	cost := q.Cost
	if cost.HP > 0 {
		if c.HP <= cost.HP {
			return nil, domain.ErrInsufficient
		}
		c.HP -= cost.HP
	}
	if cost.Meso > 0 {
		if c.Mesos < int64(cost.Meso) {
			return nil, domain.ErrInsufficient
		}
		c.Mesos -= int64(cost.Meso)
	}
	c.HP = min(c.MaxHP, c.HP+cost.HPGain)
	c.MP = min(c.MaxMP, c.MP+cost.MPGain)
	if cost.CooldownMS > 0 {
		until := now.Add(time.Duration(cost.CooldownMS) * time.Millisecond)
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_skill_cooldowns(character_id,skill_id,expires_at) VALUES($1,$2,$3) ON CONFLICT(character_id,skill_id) DO UPDATE SET expires_at=$3`, c.ID, q.SkillID, until); err != nil {
			return nil, err
		}
		c.SkillCooldowns = append(c.SkillCooldowns, domain.SkillCooldown{ID: q.SkillID, ExpiresAt: until})
	}
	if cost.Ammo == 0 {
		if cost.AmmoItemID != 0 || cost.AmmoSlot != 0 {
			return nil, domain.ErrInvalidInput
		}
		return nil, nil
	}
	for i := range c.Items {
		it := &c.Items[i]
		if it.InvType != 2 || it.Slot != cost.AmmoSlot || it.TemplateID != cost.AmmoItemID {
			continue
		}
		if it.Quantity < cost.Ammo {
			return nil, domain.ErrInsufficient
		}
		it.Quantity -= cost.Ammo
		var err error
		if it.Quantity == 0 {
			_, err = tx.ExecContext(ctx, `DELETE FROM asset_items WHERE id=$1`, it.ID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE asset_items SET quantity=$1,revision=revision+1 WHERE id=$2`, it.Quantity, it.ID)
		}
		if err != nil {
			return nil, err
		}
		out := &domain.CombatAmmoResult{Slot: it.Slot, ItemID: it.TemplateID, Quantity: it.Quantity}
		if it.Quantity == 0 {
			c.Items = append(c.Items[:i], c.Items[i+1:]...)
		}
		return out, nil
	}
	return nil, domain.ErrInsufficient
}
