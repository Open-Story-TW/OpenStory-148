package persistence

import (
	"context"
	"database/sql"
	"errors"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	_ "maple-go-server/internal/skills/all"
	"os"
	"time"
)

var _ domain.SkillRepository = (*Store)(nil)

func readSkillState(ctx context.Context, q questQuerier, c *domain.Character) error {
	rows, e := q.QueryContext(ctx, `SELECT skill_id,level,master_level,expires_at FROM game_skills WHERE character_id=$1 ORDER BY skill_id`, c.ID)
	if e != nil {
		return e
	}
	for rows.Next() {
		var s domain.SkillState
		var expiry sql.NullTime
		if e = rows.Scan(&s.ID, &s.Level, &s.MasterLevel, &expiry); e != nil {
			rows.Close()
			return e
		}
		s.ExpiresAt = expiry.Time
		c.Skills = append(c.Skills, s)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	rows, e = q.QueryContext(ctx, `SELECT skill_id,expires_at FROM game_skill_cooldowns WHERE character_id=$1 AND expires_at>clock_timestamp() ORDER BY skill_id`, c.ID)
	if e != nil {
		return e
	}
	for rows.Next() {
		var s domain.SkillCooldown
		if e = rows.Scan(&s.ID, &s.ExpiresAt); e != nil {
			rows.Close()
			return e
		}
		c.SkillCooldowns = append(c.SkillCooldowns, s)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	rows, e = q.QueryContext(ctx, `SELECT skill_id,level,stat,value,expires_at,next_tick,counter FROM game_skill_buffs WHERE character_id=$1 ORDER BY stat`, c.ID)
	if e != nil {
		return e
	}
	for rows.Next() {
		var b domain.SkillBuff
		var tick sql.NullTime
		if e = rows.Scan(&b.ID, &b.Level, &b.Stat, &b.Value, &b.ExpiresAt, &tick, &b.Counter); e != nil {
			rows.Close()
			return e
		}
		b.NextTick = tick.Time
		c.SkillBuffs = append(c.SkillBuffs, b)
	}
	e = rows.Err()
	rows.Close()
	return e
}
func learned(c domain.Character, id int) domain.SkillState {
	for _, s := range c.Skills {
		if s.ID == id {
			return s
		}
	}
	return domain.SkillState{ID: id}
}
func carrySkillVitals(c *domain.Character, session domain.CharacterSession) error {
	if c.Revision != session.Character.Revision {
		return domain.ErrStaleOwner
	}
	// Live vitals above the durable maxima (a MAXHP/MAXMP buff or passive
	// change since they were set) are clamped rather than refused; the
	// maxima themselves are always derived on the server.
	if session.Character.HP < 0 || session.Character.MP < 0 {
		return domain.ErrInvalidInput
	}
	c.HP, c.MP = min(session.Character.HP, c.MaxHP), min(session.Character.MP, c.MaxMP)
	return nil
}
func skillReplay(ctx context.Context, tx *sql.Tx, c domain.Character, op, kind string, id, level int) (bool, error) {
	if len(op) < 16 || len(op) > 128 {
		return false, domain.ErrInvalidInput
	}
	var oldKind string
	var oldID, oldLevel int
	e := tx.QueryRowContext(ctx, `SELECT kind,skill_id,client_level FROM asset_skill_commands WHERE character_id=$1 AND operation_id=$2`, c.ID, op).Scan(&oldKind, &oldID, &oldLevel)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if oldKind != kind || oldID != id || oldLevel != level {
		return false, domain.ErrForbidden
	}
	return true, nil
}
func (s *Store) finishSkill(ctx context.Context, tx *sql.Tx, r domain.SkillResult, op, kind string, level int) (domain.SkillResult, error) {
	if e := commitInventoryState(ctx, tx, &r.Character); e != nil {
		return domain.SkillResult{}, e
	}
	if op != "" {
		if _, e := tx.ExecContext(ctx, `INSERT INTO asset_skill_commands(character_id,operation_id,kind,skill_id,client_level) VALUES($1,$2,$3,$4,$5)`, r.Character.ID, op, kind, r.SkillID, level); e != nil {
			return domain.SkillResult{}, e
		}
	}
	if e := tx.Commit(); e != nil {
		return domain.SkillResult{}, e
	}
	s.roster.Invalidate(ctx, r.Character.AccountID, r.Character.WorldID)
	r.Changed = true
	return r, nil
}
func (s *Store) AllocateSkill(ctx context.Context, req domain.SkillAllocateRequest) (domain.SkillResult, error) {
	template, e := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), req.SkillID)
	if e != nil || template.Disabled {
		return domain.SkillResult{}, domain.ErrInvalidInput
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return domain.SkillResult{}, e
	}
	defer tx.Rollback()
	c, e := lockInventoryCharacter(ctx, tx, req.Session)
	if e != nil {
		return domain.SkillResult{}, e
	}
	r := domain.SkillResult{Character: c, SkillID: req.SkillID}
	replay, e := skillReplay(ctx, tx, c, req.OperationID, "allocate", req.SkillID, 0)
	if e != nil {
		return r, e
	}
	if replay {
		r.Replayed = true
		return r, nil
	}
	if e = carrySkillVitals(&c, req.Session); e != nil {
		return r, e
	}
	if !template.CanLearnAdventurer(c.Job) || c.Level < template.ReqLevel {
		return r, domain.ErrForbidden
	}
	// A newly supported passive must not derive from an ambiguous historical
	// aggregate. Keep its allocation atomic and fail before charging SP.
	if req.SkillID == 2000006 && c.VitalsModel != 1 {
		return r, domain.ErrForbidden
	}
	state := learned(c, req.SkillID)
	maximum := template.MaxLevel
	if template.MasterLevel > 0 {
		maximum = min(maximum, state.MasterLevel)
	}
	if state.Level >= maximum || template.Invisible && state.Level == 0 {
		return r, domain.ErrForbidden
	}
	var now time.Time
	if e = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		return r, e
	}
	if req.SkillID == 2000006 && !state.ExpiresAt.IsZero() && !state.ExpiresAt.After(now) {
		return r, domain.ErrForbidden
	}
	for id, level := range template.Requirements {
		prerequisite := learned(c, id)
		if id < 1 || level < 1 || prerequisite.Level < level || !prerequisite.ExpiresAt.IsZero() && !prerequisite.ExpiresAt.After(now) {
			return r, domain.ErrForbidden
		}
	}
	if req.SkillID >= 1000 && req.SkillID <= 1002 {
		used := 0
		for _, id := range []int{1000, 1001, 1002} {
			used += learned(c, id).Level
		}
		if used >= min(c.Level-1, 6) {
			return r, domain.ErrInsufficient
		}
	} else {
		// Extended SP books require their own pool; never charge the legacy pool
		// for a job family whose book semantics have not been implemented.
		if c.Job >= 600 || req.SkillID/10000 == 0 || c.SP <= 0 {
			return r, domain.ErrInsufficient
		}
		c.SP--
		if _, e = tx.ExecContext(ctx, `UPDATE game_characters SET sp=$1 WHERE id=$2`, c.SP, c.ID); e != nil {
			return r, e
		}
	}
	state.Level++
	replaced := false
	for i, v := range c.Skills {
		if v.ID == state.ID {
			c.Skills[i] = state
			replaced = true
		}
	}
	if !replaced {
		c.Skills = append(c.Skills, state)
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO game_skills(character_id,skill_id,level,master_level) VALUES($1,$2,$3,$4) ON CONFLICT(character_id,skill_id) DO UPDATE SET level=$3`, c.ID, state.ID, state.Level, state.MasterLevel); e != nil {
		return r, e
	}
	r.Character = c
	return s.finishSkill(ctx, tx, r, req.OperationID, "allocate", 0)
}
func (s *Store) CastSkill(ctx context.Context, req domain.SkillCastRequest) (domain.SkillResult, error) {
	handler, ok := skills.Lookup(req.SkillID)
	if !ok || handler.Cast == nil {
		return domain.SkillResult{}, domain.ErrInvalidInput
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return domain.SkillResult{}, e
	}
	defer tx.Rollback()
	c, e := lockInventoryCharacter(ctx, tx, req.Session)
	if e != nil {
		return domain.SkillResult{}, e
	}
	r := domain.SkillResult{Character: c, SkillID: req.SkillID}
	replay, e := skillReplay(ctx, tx, c, req.OperationID, "cast", req.SkillID, req.ClientLevel)
	if e != nil {
		return r, e
	}
	if replay {
		r.Replayed = true
		return r, nil
	}
	if e = carrySkillVitals(&c, req.Session); e != nil {
		return r, e
	}
	if c.HP <= 0 {
		return r, domain.ErrForbidden
	}
	var now time.Time
	if e = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		return r, e
	}
	levelID := req.SkillID
	if handler.LevelFrom > 0 {
		levelID = handler.LevelFrom
	}
	state := learned(c, levelID)
	// Under COMBAT_ORDERS the client sends the boosted level (Java
	// getTotalSkillLevel); CastLevel also caps a linked skill.
	level, ok := skills.CastLevel(c, levelID, req.SkillID, req.ClientLevel, now)
	if !ok {
		return r, domain.ErrForbidden
	}
	template, e := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), req.SkillID)
	if e != nil || template.Disabled || !template.CanLearnBy(c) {
		return r, domain.ErrForbidden
	}
	state.Level = level
	values, e := template.Values(state.Level)
	if e != nil {
		return r, domain.ErrInvalidInput
	}
	for _, cool := range c.SkillCooldowns {
		if cool.ID == req.SkillID && cool.ExpiresAt.After(now) {
			return r, domain.ErrAlreadyExists
		}
	}
	plan, e := handler.Cast(&skills.CastContext{Skill: template, Level: state.Level, Values: values, Caster: c, Now: now})
	if e != nil || plan.Party != nil {
		return r, domain.ErrInvalidInput
	}
	r.Effect = true
	applied, appliedLevel := req.SkillID, state.Level
	if plan.ApplyAs != 0 {
		if _, e = content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), plan.ApplyAs); e != nil || plan.ApplyLevel < 0 || plan.ApplyLevel > 255 {
			return r, domain.ErrInvalidInput
		}
		applied, r.Applied = plan.ApplyAs, plan.ApplyAs
		if plan.ApplyLevel > 0 {
			appliedLevel = plan.ApplyLevel
		}
	}
	if plan.Toggle && skills.ActiveBuff(c, applied, now) {
		// A toggle recast cancels the running buff and costs nothing.
		if e = removeSkillBuffs(ctx, tx, &c, &r, func(b domain.SkillBuff) bool { return b.ID == applied }); e != nil {
			return r, e
		}
		r.Effect = false
		r.Character = c
		return s.finishSkill(ctx, tx, r, req.OperationID, "cast", req.ClientLevel)
	}
	rows, ok := skillBuffRows(applied, appliedLevel, plan.Self, now)
	if !ok {
		return r, domain.ErrInvalidInput
	}
	if e = payCastCost(ctx, tx, &c, &r, req.SkillID, plan, now); e != nil {
		return r, e
	}
	if e = consumeBuffStats(ctx, tx, &c, &r, plan.Consume, now); e != nil {
		return r, e
	}
	if e = retimeSkillBuffs(ctx, tx, &c, &r, plan.Retime, now); e != nil {
		return r, e
	}
	if e = applyOtherSkillChanges(ctx, tx, &c, &r, req.SkillID, plan.Changes, now); e != nil {
		return r, e
	}
	if plan.MoveTo < 0 || plan.MoveTo > 999999999 {
		return r, domain.ErrInvalidInput
	}
	r.MoveTo = plan.MoveTo
	if e = applyCastEffects(ctx, tx, &c, &r, req.SkillID, plan, rows); e != nil {
		return r, e
	}
	r.Character = c
	return s.finishSkill(ctx, tx, r, req.OperationID, "cast", req.ClientLevel)
}

// skillBuffRows turns a buff into one game_skill_buffs row per stat. A nil
// buff, or one without stats, has no rows.
func skillBuffRows(id, level int, buff *skills.Buff, now time.Time) ([]domain.SkillBuff, bool) {
	if buff == nil || len(buff.Stats) == 0 {
		return nil, true
	}
	if buff.Duration < time.Second || buff.Duration > skills.Permanent || buff.Tick < 0 || buff.Tick > buff.Duration || buff.Counter < 0 || buff.Counter > 1000000 {
		return nil, false
	}
	names := make([]buffstat.Stat, len(buff.Stats))
	for i, v := range buff.Stats {
		names[i] = v.Stat
	}
	specs, err := buffstat.Resolve(names)
	if err != nil {
		return nil, false
	}
	recovery := false
	rows := make([]domain.SkillBuff, len(buff.Stats))
	for i, v := range buff.Stats {
		if v.Value < -32768 || v.Value > 32767 && !buffstat.IntValues(specs) {
			return nil, false
		}
		rows[i] = domain.SkillBuff{ID: id, Level: level, Stat: string(specs[i].Name), Value: v.Value, ExpiresAt: now.Add(buff.Duration), Counter: buff.Counter}
		if specs[i].Name == "RECOVERY" || specs[i].Name == "INFINITY" && skills.IsInfinity(id) {
			recovery = true
			rows[i].NextTick = now.Add(buff.Tick)
		}
	}
	if recovery != (buff.Tick > 0) {
		return nil, false // Only reviewed Recovery/Infinity rows have periodic effects.
	}
	return rows, true
}

// payCastCost charges the caster: MP (free under INFINITY), HP, items and
// the cooldown.
func payCastCost(ctx context.Context, tx *sql.Tx, c *domain.Character, r *domain.SkillResult, id int, plan skills.CastPlan, now time.Time) error {
	if plan.MPCost < 0 || plan.HPCost < 0 || plan.Cooldown < 0 || plan.Cooldown > 86400*time.Second || len(plan.Items) > 4 {
		return domain.ErrInvalidInput
	}
	resources, err := skills.CastResourceCost(*c, id, plan, now)
	if err != nil {
		return err
	}
	mp := resources.MP
	if c.MP < mp || plan.HPCost > 0 && c.HP <= plan.HPCost {
		return domain.ErrInsufficient
	}
	c.MP -= mp
	c.HP -= plan.HPCost
	for _, cost := range plan.Items {
		if e := consumeCastItem(ctx, tx, c, r, cost); e != nil {
			return e
		}
	}
	if resources.Cooldown > 0 {
		cool := domain.SkillCooldown{ID: id, ExpiresAt: now.Add(resources.Cooldown)}
		if _, e := tx.ExecContext(ctx, `INSERT INTO game_skill_cooldowns(character_id,skill_id,expires_at) VALUES($1,$2,$3) ON CONFLICT(character_id,skill_id) DO UPDATE SET expires_at=$3`, c.ID, cool.ID, cool.ExpiresAt); e != nil {
			return e
		}
		c.SkillCooldowns = append(c.SkillCooldowns, cool)
	}
	return nil
}

// consumeCastItem removes quantity of a non-cash item from the bags, lowest
// slots first.
func consumeCastItem(ctx context.Context, tx *sql.Tx, c *domain.Character, r *domain.SkillResult, cost skills.ItemCost) error {
	if cost.ItemID < 2000000 || cost.ItemID >= 5000000 || cost.Quantity < 1 || cost.Quantity > 1000 {
		return domain.ErrInvalidInput
	}
	have := 0
	for _, it := range c.Items {
		if it.TemplateID == cost.ItemID && it.CashID == 0 {
			have += it.Quantity
		}
	}
	if have < cost.Quantity {
		return domain.ErrInsufficient
	}
	left := cost.Quantity
	kept := make([]domain.InventoryItem, 0, len(c.Items))
	for _, it := range c.Items {
		if left == 0 || it.TemplateID != cost.ItemID || it.CashID != 0 {
			kept = append(kept, it)
			continue
		}
		take := min(left, it.Quantity)
		left -= take
		it.Quantity -= take
		var e error
		if it.Quantity == 0 {
			_, e = tx.ExecContext(ctx, `DELETE FROM asset_items WHERE id=$1`, it.ID)
			r.Inventory = append(r.Inventory, domain.InventoryChange{Mode: 3, InvType: it.InvType, Slot: it.Slot})
		} else {
			_, e = tx.ExecContext(ctx, `UPDATE asset_items SET quantity=$1,revision=revision+1 WHERE id=$2`, it.Quantity, it.ID)
			r.Inventory = append(r.Inventory, domain.InventoryChange{Mode: 1, InvType: it.InvType, Slot: it.Slot, Quantity: it.Quantity})
			kept = append(kept, it)
		}
		if e != nil {
			return e
		}
	}
	c.Items = kept
	return nil
}

// applyCastEffects applies a plan's effects to one recipient: buff rows,
// Dispel, ResetCooldowns (never the casting skill's own, Java Time Leap),
// then Heal against the new maxima.
func applyCastEffects(ctx context.Context, tx *sql.Tx, c *domain.Character, r *domain.SkillResult, id int, plan skills.CastPlan, rows []domain.SkillBuff) error {
	if c.VitalsModel != 1 && touchesMaxima(rows) {
		return domain.ErrForbidden
	}
	if len(plan.Replace) > 0 {
		replace := map[int]bool{}
		for _, other := range plan.Replace {
			replace[other] = other != id
		}
		if e := removeSkillBuffs(ctx, tx, c, r, func(b domain.SkillBuff) bool { return replace[b.ID] }); e != nil {
			return e
		}
	}
	if len(rows) > 0 {
		if e := replaceSkillBuffs(ctx, tx, c, r, rows); e != nil {
			return e
		}
	}
	if plan.Dispel {
		if e := removeSkillBuffs(ctx, tx, c, r, func(b domain.SkillBuff) bool { return buffstat.Abnormal(buffstat.Stat(b.Stat)) }); e != nil {
			return e
		}
	}
	if plan.ResetCooldowns && len(c.SkillCooldowns) > 0 {
		if _, e := tx.ExecContext(ctx, `DELETE FROM game_skill_cooldowns WHERE character_id=$1 AND skill_id<>$2`, c.ID, id); e != nil {
			return e
		}
		kept := c.SkillCooldowns[:0:0]
		for _, cool := range c.SkillCooldowns {
			if cool.ID == id {
				kept = append(kept, cool)
			} else {
				r.ResetCooldowns = append(r.ResetCooldowns, cool)
			}
		}
		c.SkillCooldowns = kept
	}
	if h := plan.Heal; h != nil {
		if h.HP < 0 || h.MP < 0 || h.HPPercent < 0 || h.HPPercent > 100 || h.MPPercent < 0 || h.MPPercent > 100 {
			return domain.ErrInvalidInput
		}
		if c.HP > 0 {
			c.HP = min(c.MaxHP, c.HP+h.HP+c.MaxHP*h.HPPercent/100)
			if !domain.IsDemonJob(c.Job) { // Demon Fury never recovers from heals.
				c.MP = min(c.MaxMP, c.MP+h.MP+c.MaxMP*h.MPPercent/100)
			}
		}
	}
	return nil
}

// consumeBuffStats lowers active buff stats in place for a cast's Consume.
func consumeBuffStats(ctx context.Context, tx *sql.Tx, c *domain.Character, r *domain.SkillResult, consume []skills.StatValue, now time.Time) error {
	for _, want := range consume {
		if want.Value < 1 {
			return domain.ErrInvalidInput
		}
		found := false
		for i, b := range c.SkillBuffs {
			if b.Stat != string(want.Stat) || !b.ExpiresAt.After(now) {
				continue
			}
			if b.Value < want.Value {
				return domain.ErrInsufficient
			}
			b.Value -= want.Value
			if _, e := tx.ExecContext(ctx, `UPDATE game_skill_buffs SET value=$1 WHERE character_id=$2 AND stat=$3 AND skill_id=$4`, b.Value, c.ID, b.Stat, b.ID); e != nil {
				return e
			}
			c.SkillBuffs[i] = b
			r.Updated = append(r.Updated, b)
			found = true
		}
		if !found {
			return domain.ErrInsufficient
		}
	}
	return nil
}

// applyOtherSkillChanges applies a cast's Changes to the caster's other
// learned skills; their new rows are reported as Updated.
func applyOtherSkillChanges(ctx context.Context, tx *sql.Tx, c *domain.Character, r *domain.SkillResult, castID int, changes []skills.SkillChange, now time.Time) error {
	for _, ch := range changes {
		state := learned(*c, ch.SkillID)
		if ch.SkillID == castID || state.Level < 1 || !state.ExpiresAt.IsZero() && !state.ExpiresAt.After(now) {
			return domain.ErrForbidden
		}
		id := ch.SkillID
		if ch.Change.Cancel {
			if e := removeSkillBuffs(ctx, tx, c, r, func(b domain.SkillBuff) bool { return b.ID == id }); e != nil {
				return e
			}
			continue
		}
		rows, ok := skillBuffRows(id, state.Level, ch.Change.Buff, now)
		if !ok || len(rows) == 0 {
			return domain.ErrInvalidInput
		}
		if e := replaceSkillBuffs(ctx, tx, c, r, rows); e != nil {
			return e
		}
		r.Updated = append(r.Updated, rows...)
	}
	return nil
}

// retimeSkillBuffs gives the caster's active rows of other skills a new
// remaining time.
func retimeSkillBuffs(ctx context.Context, tx *sql.Tx, c *domain.Character, r *domain.SkillResult, retime []skills.SkillDuration, now time.Time) error {
	for _, rt := range retime {
		if rt.Duration < time.Second || rt.Duration > skills.Permanent {
			return domain.ErrInvalidInput
		}
		for i, b := range c.SkillBuffs {
			if b.ID != rt.SkillID || !b.ExpiresAt.After(now) {
				continue
			}
			b.ExpiresAt = now.Add(rt.Duration)
			if _, e := tx.ExecContext(ctx, `UPDATE game_skill_buffs SET expires_at=$1 WHERE character_id=$2 AND stat=$3 AND skill_id=$4`, b.ExpiresAt, c.ID, b.Stat, b.ID); e != nil {
				return e
			}
			c.SkillBuffs[i] = b
			r.Updated = append(r.Updated, b)
		}
	}
	return nil
}

// removeSkillBuffs deletes every buff row matching drop.
func removeSkillBuffs(ctx context.Context, tx *sql.Tx, c *domain.Character, r *domain.SkillResult, drop func(domain.SkillBuff) bool) error {
	kept := make([]domain.SkillBuff, 0, len(c.SkillBuffs))
	for _, b := range c.SkillBuffs {
		if !drop(b) {
			kept = append(kept, b)
			continue
		}
		r.Expired = append(r.Expired, b)
		if _, e := tx.ExecContext(ctx, `DELETE FROM game_skill_buffs WHERE character_id=$1 AND stat=$2 AND skill_id=$3`, c.ID, b.Stat, b.ID); e != nil {
			return e
		}
	}
	c.SkillBuffs = kept
	return settleSkillVitals(c)
}

// replaceSkillBuffs stores rows for one character. Like Java registerEffect,
// a stat lives in only one effect: older rows with the same stat, and rows of
// the same skill that the new cast no longer provides, are expired.
//
// With MAPLEGO_STACK_BUFFS on, additive stats move to their indie form
// (buffstat.StackedForm) and an indie stat keeps the rows of other sources,
// so buffs of different skills and items stack. Rows of the same source are
// still replaced.
func replaceSkillBuffs(ctx context.Context, tx *sql.Tx, c *domain.Character, r *domain.SkillResult, rows []domain.SkillBuff) error {
	stack := domain.StackBuffsEnabled()
	if stack {
		rows = stackedRows(rows)
	}
	source := rows[0].ID
	stats := map[string]bool{}
	for _, b := range rows {
		stats[b.Stat] = true
	}
	kept := make([]domain.SkillBuff, 0, len(c.SkillBuffs)+len(rows))
	for _, old := range c.SkillBuffs {
		shared := stats[old.Stat] && !(stack && buffstat.Stackable(buffstat.Stat(old.Stat)))
		if old.ID != source && !shared {
			kept = append(kept, old)
			continue
		}
		r.Expired = append(r.Expired, old)
		// A row of the same source and stat is overwritten by the upsert.
		if old.ID != source || !stats[old.Stat] {
			if _, e := tx.ExecContext(ctx, `DELETE FROM game_skill_buffs WHERE character_id=$1 AND stat=$2 AND skill_id=$3`, c.ID, old.Stat, old.ID); e != nil {
				return e
			}
		}
	}
	for _, b := range rows {
		var next any
		if !b.NextTick.IsZero() {
			next = b.NextTick
		}
		if _, e := tx.ExecContext(ctx, `INSERT INTO game_skill_buffs(character_id,skill_id,level,stat,value,expires_at,next_tick,counter) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(character_id,stat,skill_id) DO UPDATE SET level=$3,value=$5,expires_at=$6,next_tick=$7,counter=$8`, c.ID, b.ID, b.Level, b.Stat, b.Value, b.ExpiresAt, next, b.Counter); e != nil {
			return e
		}
	}
	c.SkillBuffs = append(kept, rows...)
	return settleSkillVitals(c)
}

// stackedRows renames the rows' additive stats to their indie form in place
// (callers publish the same slice). A source that now carries one stat twice,
// e.g. WATK and ANGEL_ATK of one skill, gets the merged rows in a new slice.
func stackedRows(rows []domain.SkillBuff) []domain.SkillBuff {
	at := map[string]int{}
	var merged []domain.SkillBuff
	for i := range rows {
		rows[i].Stat = string(buffstat.StackedForm(buffstat.Stat(rows[i].Stat)))
		if j, ok := at[rows[i].Stat]; ok {
			if merged == nil {
				merged = append([]domain.SkillBuff(nil), rows[:i]...)
			}
			merged[j].Value += rows[i].Value
			continue
		}
		at[rows[i].Stat] = len(at)
		if merged != nil {
			merged = append(merged, rows[i])
		}
	}
	if merged != nil {
		return merged
	}
	return rows
}

func touchesMaxima(rows []domain.SkillBuff) bool {
	for _, b := range rows {
		if b.Stat == "MAXHP" || b.Stat == "MAXMP" {
			return true
		}
	}
	return false
}

// settleSkillVitals recomputes buff-dependent maxima (MAXHP/MAXMP) and clamps
// current HP/MP to them.
func settleSkillVitals(c *domain.Character) error {
	if c.VitalsModel != 1 {
		if touchesMaxima(c.SkillBuffs) {
			return domain.ErrForbidden
		}
		return nil
	}
	if e := recomputeVitals(c); e != nil {
		return e
	}
	c.HP, c.MP = min(c.HP, c.MaxHP), min(c.MP, c.MaxMP)
	return nil
}
func (s *Store) CancelSkill(ctx context.Context, session domain.CharacterSession, id int) (domain.SkillResult, error) {
	return s.advanceSkillBuffs(ctx, session, id, func(b domain.SkillBuff) bool { return b.ID == id && id != 0 })
}
func (s *Store) TickSkillBuffs(ctx context.Context, session domain.CharacterSession) (domain.SkillResult, error) {
	return s.advanceSkillBuffs(ctx, session, 0, func(domain.SkillBuff) bool { return false })
}

var _ domain.SkillBuffClearer = (*Store)(nil)

// ClearSkillBuffs drops every saved buff row, as a fresh login does while
// MAPLEGO_RELOG_BUFFS is off.
func (s *Store) ClearSkillBuffs(ctx context.Context, session domain.CharacterSession) (domain.SkillResult, error) {
	return s.advanceSkillBuffs(ctx, session, 0, func(domain.SkillBuff) bool { return true })
}
func (s *Store) advanceSkillBuffs(ctx context.Context, session domain.CharacterSession, cancelID int, drop func(domain.SkillBuff) bool) (domain.SkillResult, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return domain.SkillResult{}, e
	}
	defer tx.Rollback()
	c, e := lockInventoryCharacter(ctx, tx, session)
	if e != nil {
		return domain.SkillResult{}, e
	}
	r := domain.SkillResult{Character: c, SkillID: cancelID}
	if e = carrySkillVitals(&c, session); e != nil {
		return r, e
	}
	var now time.Time
	if e = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		return r, e
	}
	// A periodic percentage recovery uses today's effective maxima, including
	// another buff expiring in this transaction, never the saved old cap.
	if c.VitalsModel == 1 {
		if e = settleSkillVitals(&c); e != nil {
			return r, e
		}
	}
	kept := make([]domain.SkillBuff, 0, len(c.SkillBuffs))
	changed := false
	for _, b := range c.SkillBuffs {
		remove := drop(b) || !b.ExpiresAt.After(now) || c.HP == 0 || b.Stat == "RECOVERY" && !b.NextTick.After(now) && c.HP >= c.MaxHP
		if !remove && b.Stat == "INFINITY" && skills.IsInfinity(b.ID) && !b.NextTick.After(now) {
			var hp, mp int
			b, hp, mp, e = skills.AdvanceInfinity(os.Getenv("MAPLEGO_WZ_ROOT"), b, now, c.MaxHP, c.MaxMP)
			if e != nil {
				return r, e
			}
			c.HP, c.MP = min(c.MaxHP, c.HP+hp), min(c.MaxMP, c.MP+mp)
			if _, e = tx.ExecContext(ctx, `UPDATE game_skill_buffs SET value=$1,next_tick=$2 WHERE character_id=$3 AND stat=$4 AND skill_id=$5`, b.Value, b.NextTick, c.ID, b.Stat, b.ID); e != nil {
				return r, e
			}
			r.Updated = append(r.Updated, b)
			changed = true
		}
		if !remove && b.Stat == "RECOVERY" && !b.NextTick.After(now) {
			c.HP = min(c.MaxHP, c.HP+b.Value)
			b.NextTick = now.Add(5 * time.Second)
			changed = true
			if _, e = tx.ExecContext(ctx, `UPDATE game_skill_buffs SET next_tick=$1 WHERE character_id=$2 AND stat=$3 AND skill_id=$4`, b.NextTick, c.ID, b.Stat, b.ID); e != nil {
				return r, e
			}
		}
		if remove {
			changed = true
			r.Expired = append(r.Expired, b)
			if _, e = tx.ExecContext(ctx, `DELETE FROM game_skill_buffs WHERE character_id=$1 AND stat=$2 AND skill_id=$3`, c.ID, b.Stat, b.ID); e != nil {
				return r, e
			}
		} else {
			kept = append(kept, b)
		}
	}
	c.SkillBuffs = kept
	r.Character = c
	if !changed {
		return r, nil
	}
	if e = settleSkillVitals(&c); e != nil {
		return r, e
	}
	r.Character = c
	return s.finishSkill(ctx, tx, r, "", "", 0)
}

var _ domain.SkillTriggerRepository = (*Store)(nil)

// ChangeSkillBuffs commits trigger-driven changes to the buff rows of skills
// the character has learned.
func (s *Store) ChangeSkillBuffs(ctx context.Context, session domain.CharacterSession, changes []domain.SkillBuffChange) (domain.SkillResult, error) {
	if len(changes) == 0 || len(changes) > 16 {
		return domain.SkillResult{}, domain.ErrInvalidInput
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return domain.SkillResult{}, e
	}
	defer tx.Rollback()
	c, e := lockInventoryCharacter(ctx, tx, session)
	if e != nil {
		return domain.SkillResult{}, e
	}
	r := domain.SkillResult{Character: c}
	if e = carrySkillVitals(&c, session); e != nil {
		return r, e
	}
	if c.HP <= 0 {
		return r, domain.ErrForbidden
	}
	var now time.Time
	if e = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		return r, e
	}
	for _, ch := range changes {
		state := learned(c, ch.SkillID)
		if state.Level < 1 || state.Level != ch.Level || !state.ExpiresAt.IsZero() && !state.ExpiresAt.After(now) {
			// A received party buff may be changed by its own trigger (Soul
			// Stone on a member who never learned it), at the buff's level.
			held := false
			for _, b := range c.SkillBuffs {
				held = held || b.ID == ch.SkillID && b.Level == ch.Level && b.ExpiresAt.After(now)
			}
			if !held {
				return r, domain.ErrForbidden
			}
		}
		id := ch.SkillID
		if ch.Cooldown < 0 || ch.Cooldown > 86400*time.Second || ch.HealHP < 0 || ch.HealMP < 0 || ch.CostMP < 0 {
			return r, domain.ErrInvalidInput
		}
		c.MP = max(0, c.MP-ch.CostMP)
		c.HP = min(c.MaxHP, c.HP+ch.HealHP)
		c.MP = min(c.MaxMP, c.MP+ch.HealMP)
		if ch.Cooldown > 0 {
			cool := domain.SkillCooldown{ID: id, ExpiresAt: now.Add(ch.Cooldown)}
			if _, e = tx.ExecContext(ctx, `INSERT INTO game_skill_cooldowns(character_id,skill_id,expires_at) VALUES($1,$2,$3) ON CONFLICT(character_id,skill_id) DO UPDATE SET expires_at=$3`, c.ID, id, cool.ExpiresAt); e != nil {
				return r, e
			}
			kept := c.SkillCooldowns[:0:0]
			for _, old := range c.SkillCooldowns {
				if old.ID != id {
					kept = append(kept, old)
				}
			}
			c.SkillCooldowns = append(kept, cool)
			r.Cooldowns = append(r.Cooldowns, cool)
		}
		if ch.Cancel {
			if e = removeSkillBuffs(ctx, tx, &c, &r, func(b domain.SkillBuff) bool { return b.ID == id }); e != nil {
				return r, e
			}
			continue
		}
		if len(ch.Stats) == 0 {
			continue // A cooldown-only change.
		}
		buff := &skills.Buff{Duration: ch.Duration, Tick: ch.Tick, Counter: ch.Counter}
		for _, v := range ch.Stats {
			buff.Stats = append(buff.Stats, skills.StatValue{Stat: buffstat.Stat(v.Stat), Value: v.Value})
		}
		rows, ok := skillBuffRows(id, ch.Level, buff, now)
		if !ok || len(rows) == 0 {
			return r, domain.ErrInvalidInput
		}
		if e = applyCastEffects(ctx, tx, &c, &r, id, skills.CastPlan{}, rows); e != nil {
			return r, e
		}
	}
	r.Character = c
	return s.finishSkill(ctx, tx, r, "", "", 0)
}
