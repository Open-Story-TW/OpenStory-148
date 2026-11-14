package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"os"
	"reflect"
	"strings"
	"time"
)

var _ domain.GMRepository = (*Store)(nil)

func gmCommandHash(r domain.GMCommandRequest) string {
	return gmDigest(struct {
		AccountID, CharacterID int64
		World                  int
		Session                string
		Generation             int64
		Command                domain.GMCommandRequest
	}{r.Session.AccountID, r.Session.CharacterID, r.Session.Lease.WorldID, r.Session.SessionID, r.Session.Generation, r})
}
func gmAuditState(c domain.Character) []byte {
	v := struct {
		ID, Revision                                                                                                int64
		Level, Job, STR, DEX, INT, LUK, AP, SP, HP, MP, MaxHP, MaxMP, BaseMaxHP, BaseMaxMP, MapID, SpawnPoint, X, Y int
		EXP                                                                                                         int64
		Skills                                                                                                      []domain.SkillState
	}{c.ID, c.Revision, c.Level, c.Job, c.STR, c.DEX, c.INT, c.LUK, c.AP, c.SP, c.HP, c.MP, c.MaxHP, c.MaxMP, c.BaseMaxHP, c.BaseMaxMP, c.MapID, c.SpawnPoint, c.X, c.Y, c.EXP, c.Skills}
	raw, _ := json.Marshal(v)
	return raw
}
func gmReceipt(ctx context.Context, tx *sql.Tx, r domain.GMCommandRequest, c domain.Character, exactRevision bool) (domain.GMCommandResult, bool, error) {
	var hash string
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT request_hash,result FROM ops_qa_gm_commands WHERE character_id=$1 AND operation_id=$2`, c.ID, r.OperationID).Scan(&hash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.GMCommandResult{}, false, nil
	}
	if err != nil {
		return domain.GMCommandResult{}, false, err
	}
	if hash != gmCommandHash(r) {
		return domain.GMCommandResult{}, true, domain.ErrForbidden
	}
	var result domain.GMCommandResult
	if err = json.Unmarshal(raw, &result); err != nil {
		return result, true, err
	}
	if exactRevision && result.Character.Revision != c.Revision {
		return result, true, domain.ErrStaleOwner
	}
	result.Character = c
	result.Replayed = true
	if !exactRevision {
		result.Changed = false
		result.MapChanged = false
	}
	if result.Denied {
		return result, true, domain.ErrForbidden
	}
	return result, true, nil
}

func (s *Store) ResolveGMCommand(ctx context.Context, r domain.GMCommandRequest) (domain.GMCommandResult, bool, error) {
	if !validGMOperation(r.OperationID) {
		return domain.GMCommandResult{}, false, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.GMCommandResult{}, false, err
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, r.Session)
	if err != nil {
		return domain.GMCommandResult{}, false, err
	}
	if r.Name == "spawn" || r.Name == "clearmobs" {
		grant, e := readQAGMGrant(ctx, tx, c.ID)
		if e != nil {
			return domain.GMCommandResult{}, false, e
		}
		var now time.Time
		if e = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
			return domain.GMCommandResult{}, false, e
		}
		if !validQAGMGrant(grant, c, now) {
			return domain.GMCommandResult{}, false, domain.ErrForbidden
		}
	}
	return gmReceipt(ctx, tx, r, c, true)
}

func (s *Store) ExecuteGMCommand(ctx context.Context, r domain.GMCommandRequest) (domain.GMCommandResult, error) {
	if !validGMOperation(r.OperationID) || len(r.Name) < 1 || len(r.Name) > 32 || len(r.Args) > 4 || len(r.Text) > domain.GMNoticeMax {
		return domain.GMCommandResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.GMCommandResult{}, err
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, r.Session)
	if err != nil {
		return domain.GMCommandResult{}, err
	}
	result := domain.GMCommandResult{Character: c, Name: r.Name, Args: append([]int64(nil), r.Args...), Text: r.Text}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return result, err
	}
	grant, grantErr := readQAGMGrant(ctx, tx, c.ID)
	if grantErr != nil && !errors.Is(grantErr, sql.ErrNoRows) {
		return result, grantErr
	}
	if old, found, e := gmReceipt(ctx, tx, r, c, false); e != nil || found {
		if !validQAGMGrant(grant, c, now) {
			old.Denied = true
			old.Reason = "not_authorized"
			return old, domain.ErrForbidden
		}
		return old, e
	}
	before := gmAuditState(c)
	finish := func(denied string) (domain.GMCommandResult, error) {
		outcome := "accepted"
		if denied != "" {
			result.Denied = true
			result.Reason = denied
			result.Changed = false
			result.MapChanged = false
			outcome = "denied"
		}
		raw, e := json.Marshal(result)
		if e != nil {
			return result, e
		}
		args, e := json.Marshal(r.Args)
		if e != nil {
			return result, e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO ops_qa_gm_commands(character_id,operation_id,account_id,world_id,session_id,generation,request_hash,command,args,grant_revision,outcome,before_state,after_state,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, c.ID, r.OperationID, r.Session.AccountID, r.Session.Lease.WorldID, r.Session.SessionID, r.Session.Generation, gmCommandHash(r), r.Name, args, grant.Revision, outcome, before, gmAuditState(result.Character), raw); e != nil {
			return result, e
		}
		if e = tx.Commit(); e != nil {
			return result, e
		}
		if result.Changed {
			s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
		}
		if result.Denied {
			return result, domain.ErrForbidden
		}
		return result, nil
	}
	// Even a forged live GM value is insufficient: both fresh persisted rank and
	// the exact expiring dedicated-character capability are checked under locks.
	if !validQAGMGrant(grant, c, now) {
		return finish("not_authorized")
	}
	if c.Revision != r.Session.Character.Revision {
		return result, domain.ErrStaleOwner
	}
	if err = carrySkillVitals(&c, r.Session); err != nil {
		return result, err
	}
	if c.MapID != r.Session.Character.MapID {
		return result, domain.ErrStaleOwner
	}
	c.X, c.Y, c.SpawnPoint = r.Session.Character.X, r.Session.Character.Y, r.Session.Character.SpawnPoint
	result.Character = c
	before = gmAuditState(c)
	result.Standing = !grant.ExpiresAt.Before(domain.QAGMPermanentExpiry)
	if domain.GMStandingCommand(r.Name) {
		// Authorization and audit only: the world acts after this commit.
		if !result.Standing {
			result.Detail = "this command needs a standing GM grant from the web admin"
			return finish("standing_grant_required")
		}
		if !domain.ValidGMStandingCommand(r.Name, r.Args, r.Text) {
			return finish("unsupported_or_invalid_command")
		}
		if r.Name == "hide" {
			// The GM Hide skill's own buff row, so the skill can cancel it too.
			var buffs domain.SkillResult
			var e error
			if r.Args[0] == 1 {
				e = replaceSkillBuffs(ctx, tx, &c, &buffs, []domain.SkillBuff{{ID: 9001004, Level: 1, Stat: "GM_HIDE", Value: 1, ExpiresAt: now.Add(12 * time.Hour)}})
			} else {
				e = removeSkillBuffs(ctx, tx, &c, &buffs, func(b domain.SkillBuff) bool { return b.Stat == "GM_HIDE" })
			}
			if e == nil {
				e = commitInventoryState(ctx, tx, &c)
			}
			if e != nil {
				return result, e
			}
			result.Character = c
			result.Changed = true
		}
		return finish("")
	}
	if r.Text != "" {
		return finish("unsupported_or_invalid_command")
	}
	if r.Name == "debuff" {
		// QA self-debuff to test dispels and heals: a monster-skill status
		// (Java giveDebuff: source = mob skill | level<<16) for N seconds.
		if len(r.Args) < 1 || len(r.Args) > 2 || r.Args[0] < 0 || r.Args[0] > 255 {
			return finish("unsupported_or_invalid_command")
		}
		spec, ok := buffstat.ByBit(int(r.Args[0]))
		mobSkill := qaDebuffSkills[string(spec.Name)]
		seconds := int64(30)
		if len(r.Args) == 2 {
			seconds = r.Args[1]
		}
		if !ok || mobSkill == 0 || seconds < 1 || seconds > 600 {
			return finish("unsupported_or_invalid_command")
		}
		value := 1
		if spec.Name == "POISON" {
			value = 10
		}
		row := domain.SkillBuff{ID: mobSkill | 1<<16, Level: 1, Stat: string(spec.Name), Value: value, ExpiresAt: now.Add(time.Duration(seconds) * time.Second)}
		var replaced domain.SkillResult
		if e := replaceSkillBuffs(ctx, tx, &c, &replaced, []domain.SkillBuff{row}); e != nil {
			return result, e
		}
		if e := commitInventoryState(ctx, tx, &c); e != nil {
			return result, e
		}
		result.Character = c
		result.Changed = true
		return finish("")
	}
	if r.Name == "buffreset" {
		// QA recovery for a client stuck in a buff state: remove every row of
		// the stat; the world then sends its reset whether or not one existed.
		if len(r.Args) != 1 || r.Args[0] < 0 || r.Args[0] > 255 {
			return finish("unsupported_or_invalid_command")
		}
		spec, ok := buffstat.ByBit(int(r.Args[0]))
		if !ok {
			return finish("unsupported_or_invalid_command")
		}
		names := map[string]bool{string(spec.Name): true}
		for _, alias := range spec.Aliases {
			names[string(alias)] = true
		}
		var removed domain.SkillResult
		if e := removeSkillBuffs(ctx, tx, &c, &removed, func(b domain.SkillBuff) bool { return names[b.Stat] }); e != nil {
			return result, e
		}
		if e := commitInventoryState(ctx, tx, &c); e != nil {
			return result, e
		}
		result.Character = c
		result.Changed = true
		return finish("")
	}
	if r.Name == "petfood" || r.Name == "petexpire" || r.Name == "mesos" || r.Name == "alliance" || r.Name == "item" {
		var e error
		if r.Name == "item" {
			e = applyQAItem(ctx, tx, &c, r, &result)
		} else {
			e = applyQAPetSetup(ctx, tx, &c, r, now, &result)
		}
		if errors.Is(e, domain.ErrInvalidInput) || errors.Is(e, domain.ErrInsufficient) {
			return finish("unsupported_or_invalid_command")
		}
		if e != nil {
			return result, e
		}
		result.Character = c
		result.Changed = r.Name != "alliance"
		return finish("")
	}
	if r.Name == "cashexpire" {
		expiry, e := applyQACashExpiry(ctx, tx, c, r, now)
		if errors.Is(e, domain.ErrInvalidInput) {
			return finish("unsupported_or_invalid_command")
		}
		if e != nil {
			return result, e
		}
		result.CashExpiry = &expiry
		return finish("")
	}
	if r.Name == "spawn" || r.Name == "clearmobs" {
		result.QAMobs, err = authorizeQAMobs(c, r, grant, now)
		if errors.Is(err, domain.ErrInvalidInput) || errors.Is(err, domain.ErrForbidden) {
			return finish("unsupported_or_invalid_command")
		}
		if err != nil {
			return result, err
		}
		return finish("")
	}
	priorSkills := c.Skills
	changed, skillID, mapChanged, policyErr := applyQAGMPolicy(&c, r, now)
	if policyErr == nil && (c.Level < 1 || c.Level > domain.MaxCharacterLevel() || c.AP < 0 || c.AP > max(65535, domain.MaxStat()) || c.SP < 0 || c.SP > 65535 || c.STR < 4 || c.STR > domain.MaxStat() || c.DEX < 4 || c.DEX > domain.MaxStat() || c.INT < 4 || c.INT > domain.MaxStat() || c.LUK < 4 || c.LUK > domain.MaxStat()) {
		policyErr = domain.ErrInvalidInput
	}
	if policyErr != nil {
		if _, detail, ok := strings.Cut(policyErr.Error(), domain.ErrInvalidInput.Error()+": "); ok {
			result.Detail = detail
		}
		return finish("unsupported_or_invalid_command")
	}
	result.SkillID = skillID
	result.MapChanged = mapChanged
	result.Changed = changed
	if changed {
		if skillID != 0 {
			state := learned(c, skillID)
			if _, err = tx.ExecContext(ctx, `INSERT INTO game_skills(character_id,skill_id,level,master_level) VALUES($1,$2,$3,$4) ON CONFLICT(character_id,skill_id) DO UPDATE SET level=EXCLUDED.level,master_level=EXCLUDED.master_level`, c.ID, state.ID, state.Level, state.MasterLevel); err != nil {
				return result, err
			}
		}
		if r.Name == "maxskills" {
			prior := domain.Character{Skills: priorSkills}
			for _, state := range c.Skills {
				if learned(prior, state.ID) == state {
					continue
				}
				if _, err = tx.ExecContext(ctx, `INSERT INTO game_skills(character_id,skill_id,level,master_level) VALUES($1,$2,$3,$4) ON CONFLICT(character_id,skill_id) DO UPDATE SET level=EXCLUDED.level,master_level=EXCLUDED.master_level`, c.ID, state.ID, state.Level, state.MasterLevel); err != nil {
					return result, err
				}
				result.SkillIDs = append(result.SkillIDs, state.ID)
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE game_characters SET hair=$1,face=$2,skin=$3,fame=$4 WHERE id=$5`, c.Hair, c.Face, c.Skin, c.Fame, c.ID); err != nil {
			return result, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE game_characters SET level=$1,job=$2,str=$3,dex=$4,int_stat=$5,luk=$6,ap=$7,sp=$8,exp=$9,map_id=$10,spawn_point=$11,x=$12,y=$13 WHERE id=$14`, c.Level, c.Job, c.STR, c.DEX, c.INT, c.LUK, c.AP, c.SP, c.EXP, c.MapID, c.SpawnPoint, c.X, c.Y, c.ID); err != nil {
			return result, err
		}
		if err = saveJobState(ctx, tx, c); err != nil {
			return result, err
		}
		if err = commitInventoryState(ctx, tx, &c); err != nil {
			return result, err
		}
	}
	result.Character = c
	return finish("")
}

// qaDebuffSkills are the Java MobSkill IDs of the !debuff statuses.
var qaDebuffSkills = map[string]int{
	"SEAL": 120, "DARKNESS": 121, "WEAKEN": 122, "STUN": 123, "CURSE": 124, "POISON": 125, "SLOW": 126,
	"SEDUCE": 128, "REVERSE_DIRECTION": 132, "ZOMBIFY": 133, "POTION": 134, "SHADOW": 135, "BLIND": 136, "FREEZE": 137,
}

// Only reviewed existing effects are exposed. !skill is an explicit QA
// extension: it changes learned state without charging SP, and never casts.
func applyQAGMPolicy(c *domain.Character, r domain.GMCommandRequest, now time.Time) (changed bool, skillID int, mapChanged bool, err error) {
	before := *c
	invalid := func() (bool, int, bool, error) { return false, 0, false, domain.ErrInvalidInput }
	deny := func(reason string) (bool, int, bool, error) {
		return false, 0, false, fmt.Errorf("%w: %s", domain.ErrInvalidInput, reason)
	}
	if r.Name == "whereami" || r.Name == "effstats" || r.Name == "online" || r.Name == "rates" {
		if len(r.Args) != 0 {
			return invalid()
		}
		return false, 0, false, nil
	}
	if r.Name == "help" {
		// !help, or !help <page>: the world checks the page number.
		if len(r.Args) > 1 {
			return invalid()
		}
		return false, 0, false, nil
	}
	if c.HP <= 0 {
		return invalid()
	}
	if !domain.ValidJob(c.Job) {
		return invalid()
	}
	switch r.Name {
	case "heal":
		if len(r.Args) != 0 {
			return invalid()
		}
		c.HP, c.MP = c.MaxHP, c.MaxMP
	case "hp":
		// QA HP setter (0 runs the ordinary death flow in the world).
		if len(r.Args) != 1 || r.Args[0] < 0 || r.Args[0] > int64(c.MaxHP) {
			return invalid()
		}
		c.HP = int(r.Args[0])
	case "level":
		if len(r.Args) != 1 || r.Args[0] < int64(c.Level) || r.Args[0] > int64(domain.MaxCharacterLevel()) {
			return invalid()
		}
		if r.Args[0] == int64(c.Level) {
			return false, 0, false, nil
		}
		for c.Level < int(r.Args[0]) {
			need := content.ExperienceForLevel(c.Level) - c.EXP
			if need < 1 {
				return invalid()
			}
			if e := applyQuestExperience(c, need, fmt.Sprintf("%s/%d", r.OperationID, c.Level)); e != nil {
				return false, 0, false, e
			}
		}
		c.EXP = 0
	case "job":
		// Raymond approved QA job setting for skill acceptance (any v145 job).
		// A beginner's first Explorer job keeps the native advancement stats.
		if len(r.Args) != 1 || r.Args[0] < 0 || r.Args[0] > 9999 || !domain.ValidJob(int(r.Args[0])) {
			return invalid()
		}
		job := int(r.Args[0])
		if c.Job == job {
			return false, 0, false, nil
		}
		if c.Job == 0 && job%100 == 0 && job >= 100 && job <= 500 {
			minimum := 10
			if job == 200 {
				minimum = 8
			}
			next, e := firstJobStats(*c, r.OperationID, firstJobPolicy{job: job, minLevel: minimum})
			if e != nil {
				return false, 0, false, e
			}
			*c = next
			break
		}
		c.SetJob(job)
		c.SubJob = domain.SubJobFor(job)
		if c.VitalsModel == 1 {
			if e := recomputeVitals(c); e != nil {
				return false, 0, false, e
			}
		}
		c.HP, c.MP = min(c.HP, c.MaxHP), min(c.MP, c.MaxMP)
	case "skill":
		if len(r.Args) != 2 || r.Args[0] < 1 || r.Args[0] > 99999999 || r.Args[1] < 1 || r.Args[1] > 255 {
			return invalid()
		}
		id, level := int(r.Args[0]), int(r.Args[1])
		if _, registered := skills.Lookup(id); !registered {
			return deny(fmt.Sprintf("skill %d has no registered handler", id))
		}
		if id == 2000006 && c.VitalsModel != 1 {
			return invalid()
		}
		template, e := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), id)
		// Registered invisible skills (Maple Warrior, Hero's Will) are learned
		// from books or quests in play; QA may grant them like any other.
		switch {
		case e != nil || template.Disabled:
			return deny(fmt.Sprintf("skill %d is not a usable WZ skill", id))
		case !template.CanLearnBy(*c):
			return deny(fmt.Sprintf("job %d cannot learn skill %d", c.Job, id))
		case c.Level < template.ReqLevel:
			return deny(fmt.Sprintf("skill %d needs level %d", id, template.ReqLevel))
		case level > template.MaxLevel:
			return deny(fmt.Sprintf("skill %d max level is %d", id, template.MaxLevel))
		}
		if _, e = template.Values(level); e != nil {
			return invalid()
		}
		state := learned(*c, id)
		if state.Level > level || !state.ExpiresAt.IsZero() && !state.ExpiresAt.After(now) {
			return invalid()
		}
		// A master-level skill needs its skill book in play; QA raises the
		// master level to the requested level instead (audited gm_command).
		if template.MasterLevel > 0 && level > state.MasterLevel {
			state.MasterLevel = level
		}
		if state.Level == level {
			return false, id, false, nil
		}
		for _, b := range c.SkillBuffs {
			if b.ID == id && b.ExpiresAt.After(now) {
				return deny(fmt.Sprintf("skill %d buff is active", id))
			}
		}
		for _, b := range c.SkillCooldowns {
			if b.ID == id && b.ExpiresAt.After(now) {
				return deny(fmt.Sprintf("skill %d is cooling down", id))
			}
		}
		for prerequisite, needed := range template.Requirements {
			p := learned(*c, prerequisite)
			if p.Level < needed || !p.ExpiresAt.IsZero() && !p.ExpiresAt.After(now) {
				return deny(fmt.Sprintf("skill %d needs skill %d level %d first", id, prerequisite, needed))
			}
		}
		state.Level = level
		replaced := false
		next := append([]domain.SkillState(nil), c.Skills...)
		for i, s := range next {
			if s.ID == id {
				next[i] = state
				replaced = true
			}
		}
		if !replaced {
			next = append(next, state)
		}
		c.Skills = next
		skillID = id
		if c.VitalsModel == 1 {
			if e = recomputeVitals(c); e != nil {
				return false, 0, false, e
			}
		}
	case "stats":
		// !stats <str> <dex> <int> <luk>: QA sets base abilities so job
		// weapons' reqSTR/DEX/INT/LUK (the client checks them) can be met.
		if len(r.Args) != 4 {
			return invalid()
		}
		for _, v := range r.Args {
			if v < 4 || v > int64(domain.MaxStat()) {
				return deny(fmt.Sprintf("abilities must be 4..%d", domain.MaxStat()))
			}
		}
		c.STR, c.DEX, c.INT, c.LUK = int(r.Args[0]), int(r.Args[1]), int(r.Args[2]), int(r.Args[3])
		if c.VitalsModel == 1 {
			if e := recomputeVitals(c); e != nil {
				return false, 0, false, e
			}
		}
	case "hair", "face":
		// !hair / !face <style>: any style the client has, no coupon.
		if len(r.Args) != 1 {
			return invalid()
		}
		id := int(r.Args[0])
		low, high := 30000, 49999
		if r.Name == "face" {
			low, high = 20000, 29999
		}
		if id < low || id > high {
			return deny(fmt.Sprintf("%s must be %d..%d", r.Name, low, high))
		}
		if !content.StyleExists(os.Getenv("MAPLEGO_WZ_ROOT"), id) {
			return deny(fmt.Sprintf("%s %d does not exist", r.Name, id))
		}
		if r.Name == "face" {
			c.Face = id
		} else {
			c.Hair = id
		}
	case "skin":
		if len(r.Args) != 1 || r.Args[0] < 0 || r.Args[0] > 11 || r.Args[0] > 5 && r.Args[0] < 9 {
			return deny("skin must be 0..5 or 9..11")
		}
		c.Skin = int(r.Args[0])
	case "fame":
		if len(r.Args) != 1 || r.Args[0] < 0 || r.Args[0] > 30000 {
			return deny("fame must be 0..30000")
		}
		c.Fame = int(r.Args[0])
	case "maxskills":
		// Every registered job skill the current job can learn at this level, to
		// its top level. A skill whose buff or cooldown is running is left.
		if len(r.Args) != 0 {
			return invalid()
		}
		next := append([]domain.SkillState(nil), c.Skills...)
		root := os.Getenv("MAPLEGO_WZ_ROOT")
		for _, id := range skills.IDs() {
			// Job books only: beginner books hold event and mount skills, and
			// 800+ are GM, profession and special books.
			if book := id / 10000; book%1000 < 100 || book >= 8000 || book/100 == 8 || book/100 == 9 && c.Job/100 != 9 {
				continue
			}
			template, e := content.SkillDefinition(root, id)
			if e != nil || template.Disabled || !template.CanLearnBy(*c) || c.Level < template.ReqLevel || id == 2000006 && c.VitalsModel != 1 {
				continue
			}
			if _, e = template.Values(template.MaxLevel); e != nil {
				continue
			}
			busy := false
			for _, b := range c.SkillBuffs {
				busy = busy || b.ID == id && b.ExpiresAt.After(now)
			}
			for _, b := range c.SkillCooldowns {
				busy = busy || b.ID == id && b.ExpiresAt.After(now)
			}
			state := learned(*c, id)
			if busy || !state.ExpiresAt.IsZero() && !state.ExpiresAt.After(now) || state.Level >= template.MaxLevel {
				continue
			}
			state.Level = template.MaxLevel
			if template.MasterLevel > 0 && state.MasterLevel < template.MaxLevel {
				state.MasterLevel = template.MaxLevel
			}
			replaced := false
			for i, s := range next {
				if s.ID == id {
					next[i], replaced = state, true
				}
			}
			if !replaced {
				next = append(next, state)
			}
		}
		c.Skills = next
		if c.VitalsModel == 1 {
			if e := recomputeVitals(c); e != nil {
				return false, 0, false, e
			}
		}
	case "ap":
		// !ap <points>: sets the unspent ability points.
		if len(r.Args) != 1 || r.Args[0] < 0 || r.Args[0] > 32767 {
			return deny("AP must be 0..32767")
		}
		c.AP = int(r.Args[0])
	case "sp":
		// !sp <points>: sets the unspent skill points of the current job.
		if len(r.Args) != 1 || r.Args[0] < 0 || r.Args[0] > 1000 {
			return deny("SP must be 0..1000")
		}
		c.SP = int(r.Args[0])
	case "vitals":
		// !vitals <maxhp> <maxmp>: QA sets the base max HP and MP (before
		// equipment and passives), then fills both.
		if len(r.Args) != 2 || c.VitalsModel != 1 {
			return invalid()
		}
		if r.Args[0] < 1 || r.Args[0] > int64(domain.MaxVital()) || r.Args[1] < 0 || r.Args[1] > int64(domain.MaxVital()) {
			return deny(fmt.Sprintf("max HP must be 1..%d and max MP 0..%d", domain.MaxVital(), domain.MaxVital()))
		}
		c.BaseMaxHP, c.BaseMaxMP = int(r.Args[0]), int(r.Args[1])
		if e := recomputeVitals(c); e != nil {
			return false, 0, false, e
		}
		c.HP, c.MP = c.MaxHP, c.MaxMP
	case "mobhold":
		// Authorization and audit only: the world holds this field's mobs.
		if len(r.Args) != 1 || r.Args[0] < 0 || r.Args[0] > 1 {
			return invalid()
		}
		if domain.QAWarpMaps[c.MapID] == "" {
			return deny("mobhold runs on QA test fields only")
		}
		return false, 0, false, nil
	case "warp":
		if len(r.Args) != 1 || r.Args[0] < 0 || r.Args[0] > 999999998 || domain.QAWarpMaps[int(r.Args[0])] == "" {
			return invalid()
		}
		catalog, e := content.DefaultCatalog()
		if e != nil {
			return false, 0, false, e
		}
		m, e := catalog.Map(int(r.Args[0]))
		if e != nil {
			return invalid()
		}
		p, ok := m.Spawn()
		if !ok || p.ID < 0 || p.ID > 255 {
			return invalid()
		}
		// The same map is a valid target too: the world moves the character
		// back to the spawn point (QA position reset).
		// Authorize and audit only: the world performs the fenced field
		// transition (warpGameplayConditional) after this commit.
		return false, 0, true, nil
	default:
		return invalid()
	}
	return !reflect.DeepEqual(before, *c), skillID, mapChanged, nil
}

// !cashexpire rewrites one owned, non-pet locker record's expiry so QA can
// observe the ordinary cash-shop entry expiry path. It never touches items in
// a bag or storage, another account's locker, or a pet's separate lifetime.
// The world session row lock (state='world') excludes a concurrent cash stage.
func applyQACashExpiry(ctx context.Context, tx *sql.Tx, c domain.Character, r domain.GMCommandRequest, now time.Time) (domain.GMCashExpiry, error) {
	if len(r.Args) != 2 || r.Args[0] < 1 || r.Args[1] < 0 || r.Args[1] > domain.QACashExpiryMaxSeconds {
		return domain.GMCashExpiry{}, domain.ErrInvalidInput
	}
	v := domain.GMCashExpiry{CashID: r.Args[0]}
	err := tx.QueryRowContext(ctx, `SELECT template_id,expires_at FROM asset_cash_items WHERE id=$1 AND account_id=$2 AND state='locker' FOR UPDATE`, v.CashID, c.AccountID).Scan(&v.TemplateID, &v.PreviousExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return v, domain.ErrInvalidInput
	}
	if err != nil {
		return v, err
	}
	if v.TemplateID/10000 == 500 {
		return v, domain.ErrInvalidInput
	}
	v.ExpiresAt = now.UnixMilli() + r.Args[1]*1000
	if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_items SET expires_at=$1 WHERE id=$2 AND account_id=$3 AND state='locker'`, v.ExpiresAt, v.CashID, c.AccountID); err != nil {
		return v, err
	}
	return v, nil
}

// applyQAPetSetup serves the pet acceptance setup commands. !petfood grants
// ordinary pet food to the caller's USE bag; !petexpire moves the lifetime of
// one pet in the caller's CASH bag so the normal expiry recall can be observed.
func applyQAPetSetup(ctx context.Context, tx *sql.Tx, c *domain.Character, r domain.GMCommandRequest, now time.Time, result *domain.GMCommandResult) error {
	switch r.Name {
	case "petfood":
		if len(r.Args) != 1 || r.Args[0] < 1 || r.Args[0] > domain.QAPetFoodMax {
			return domain.ErrInvalidInput
		}
		inv := domain.InventoryResult{Character: *c}
		if err := pickupBundle(ctx, tx, domain.ItemDrop{InvType: 2, TemplateID: domain.QAPetFoodItem, Quantity: int(r.Args[0])}, &inv); err != nil {
			return err
		}
		if err := commitInventoryState(ctx, tx, &inv.Character); err != nil {
			return err
		}
		*c = inv.Character
		result.Inventory = inv.Changes
		return nil
	case "alliance":
		// Authorization and audit only: the world then runs the ordinary
		// alliance create (partner > 0) or disband (0) command without the NPC.
		if len(r.Args) != 1 || r.Args[0] < 0 || r.Args[0] == c.ID {
			return domain.ErrInvalidInput
		}
		return nil
	case "mesos":
		// Setup funds for guild/alliance fees, through the meso ledger.
		if len(r.Args) != 1 || r.Args[0] < 1 || r.Args[0] > domain.QAMesosMax || r.Args[0] > domain.MaxMoney()-c.Mesos {
			return domain.ErrInvalidInput
		}
		c.Mesos += r.Args[0]
		c.Revision++
		if _, err := tx.ExecContext(ctx, `UPDATE game_characters SET mesos=$1,revision=$2 WHERE id=$3`, c.Mesos, c.Revision, c.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO asset_meso_ledger(operation_id,character_id,account_id,world_id,delta,balance_after,reason) VALUES($1,$2,$3,$4,$5,$6,'qa_gm')`, r.OperationID, c.ID, c.AccountID, c.WorldID, r.Args[0], c.Mesos)
		return err
	case "petexpire":
		if len(r.Args) != 2 || r.Args[0] < 1 || r.Args[1] < 0 || r.Args[1] > domain.QAPetExpiryMax {
			return domain.ErrInvalidInput
		}
		v := domain.GMCashExpiry{CashID: r.Args[0]}
		err := tx.QueryRowContext(ctx, `SELECT template_id,expires_at FROM asset_cash_items WHERE id=$1 AND account_id=$2 AND character_id=$3 AND state='inventory' FOR UPDATE`, v.CashID, c.AccountID, c.ID).Scan(&v.TemplateID, &v.PreviousExpiresAt)
		if errors.Is(err, sql.ErrNoRows) || err == nil && !domain.IsPetTemplate(v.TemplateID) {
			return domain.ErrInvalidInput
		}
		if err != nil {
			return err
		}
		v.ExpiresAt = now.UnixMilli() + r.Args[1]*1000
		if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_items SET expires_at=$1 WHERE id=$2`, v.ExpiresAt, v.CashID); err != nil {
			return err
		}
		for i := range c.Items {
			if c.Items[i].CashID == v.CashID {
				c.Items[i].ExpiresAt = v.ExpiresAt
			}
		}
		for i := range c.Pets {
			if c.Pets[i].CashID == v.CashID {
				c.Pets[i].ExpiresAt = v.ExpiresAt
			}
		}
		result.PetExpiry = &v
		return nil
	}
	return domain.ErrInvalidInput
}
