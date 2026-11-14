package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

type firstJobReward struct {
	id, count int
	fresh     bool
}
type firstJobPolicy struct {
	job, minLevel, mapID int
	// fromJob is the beginner job the advancement starts from (0 Explorer,
	// 1000 Noblesse, 2000 Aran, 3000 Citizen).
	fromJob int
	// subJob, when not -1, is the Explorer subcategory the character must
	// already have (2 for the Cannoneer path).
	subJob int
	// expand lists the inventories that grow by four slots.
	expand          []int
	postPirateReset bool
	// base is the STR/DEX/INT/LUK the reset leaves (zero: four each).
	base    [4]int
	rewards []firstJobReward
	// quests are the quest states the advancement requires (a quest source
	// advances inside its own quest), and complete the quests it finishes in
	// the same transaction.
	quests   map[int]int
	complete []int
	skills   []domain.SkillState
	// verify is a quest whose WZ completion demand must hold; consume are
	// the items taken with the advancement.
	verify  int
	consume []firstJobReward
}

func firstJobPolicyFor(r domain.JobAdvancementRequest) (firstJobPolicy, error) {
	p := firstJobPolicy{job: r.TargetJob, minLevel: 10, subJob: -1}
	if r.Script == "" || len(r.OperationID) < 16 || len(r.OperationID) > 128 {
		return p, domain.ErrForbidden
	}
	switch r.NPCID {
	case 1022000:
		if r.Script != "npc/1022000.lua" || r.TargetJob != 100 {
			return p, domain.ErrForbidden
		}
		p.mapID, p.expand, p.rewards = 102000003, []int{1, 4}, []firstJobReward{{1402001, 1, false}}
	case 1032001:
		if r.Script != "npc/1032001.lua" || r.TargetJob != 200 {
			return p, domain.ErrForbidden
		}
		p.mapID, p.minLevel, p.expand, p.rewards = 101000003, 8, []int{1, 4}, []firstJobReward{{1372005, 1, false}}
	case 1012100:
		if r.Script != "npc/1012100.lua" || r.TargetJob != 300 {
			return p, domain.ErrForbidden
		}
		p.mapID, p.expand, p.rewards = 100000201, []int{1, 4}, []firstJobReward{{1452002, 1, false}, {2060000, 1000, false}}
	case 1052001:
		if r.Script != "npc/1052001.lua" || (r.TargetJob != 100 && r.TargetJob != 200 && r.TargetJob != 300 && r.TargetJob != 400 && r.TargetJob != 500) {
			return p, domain.ErrForbidden
		}
		// This baseline replaced Dark Lord with a general first-job menu.
		// It calls changeJob only; do not invent gifts or bag expansion.
		p.mapID = 103000003
	case 1090000:
		if r.Script == "quest/2570.lua" {
			return questJobPolicyFor(r)
		}
		if r.Script != "npc/1090000.lua" || r.TargetJob != 500 {
			return p, domain.ErrForbidden
		}
		p.mapID, p.postPirateReset, p.rewards = 120000101, true, []firstJobReward{{1482014, 1, false}, {1492014, 1, false}, {2330006, 600, true}, {2330006, 600, true}, {2330006, 600, true}}
	default:
		return questJobPolicyFor(r)
	}
	return p, nil
}

func firstJobStats(c domain.Character, operationID string, p firstJobPolicy) (domain.Character, error) {
	if c.Job != p.fromJob || c.Level < p.minLevel || c.Level > domain.MaxCharacterLevel() || c.HP <= 0 || c.HP > c.MaxHP || c.MP < 0 || c.MP > c.MaxMP {
		return c, domain.ErrForbidden
	}
	if c.STR < 4 || c.DEX < 4 || c.INT < 4 || c.LUK < 4 || c.AP < 0 || c.SP < 0 || c.SP >= 65535 || len(operationID) < 16 || len(operationID) > 128 {
		return c, domain.ErrInvalidInput
	}
	total := int64(c.STR) + int64(c.DEX) + int64(c.INT) + int64(c.LUK) + int64(c.AP)
	reset := domain.IsBeginnerJob(p.fromJob)
	base := p.base
	if base == [4]int{} {
		base = [4]int{4, 4, 4, 4}
	}
	if p.postPirateReset {
		// The pirate instructor's script resets DEX to 20 after changeJob.
		base[1] = 20
	}
	resetTotal := base[0] + base[1] + base[2] + base[3]
	if !reset {
		base, resetTotal = [4]int{c.STR, c.DEX, c.INT, c.LUK}, int(total)-c.AP
	}
	if total < int64(resetTotal) || total-int64(resetTotal) > int64(domain.MaxStat()) || c.MaxHP < 1 || c.MaxHP > domain.MaxVital() || c.MaxMP < 0 || c.MaxMP > domain.MaxVital() {
		return c, domain.ErrInvalidInput
	}
	c.SetJob(p.job)
	c.STR, c.DEX, c.INT, c.LUK, c.AP = base[0], base[1], base[2], base[3], int(total)-resetTotal
	// Legacy changeJob: separated-SP jobs (Evan, Resistance) get five SP in
	// the new book, every other job one.
	if domain.ExtendedSPJob(p.job) {
		c.SP += 5
	} else {
		c.SP++
	}
	seed := sha256.Sum256([]byte(operationID))
	hpDraw, mpDraw := binary.LittleEndian.Uint64(seed[:8]), binary.LittleEndian.Uint64(seed[8:16])
	hpGain, mpGain := 0, 0
	// Legacy changeJob's table. It leaves out 501 and Cygnus 1200-1500, which
	// take the gain of the Explorer class they mirror here.
	switch p.job {
	case 100, 1100, 2100, 3200:
		hpGain = 200 + int(hpDraw%51)
	case 200, 1200:
		mpGain = 100 + int(hpDraw%51)
	case 300, 400, 500, 501, 1300, 1400, 1500, 3300, 3500:
		hpGain = 100 + int(hpDraw%51)
		mpGain = 25 + int(mpDraw%26)
	case 430:
		// Legacy adds both draws of this row to max HP.
		hpGain = 200 + int(hpDraw%51) + 150 + int(mpDraw%51)
	case 431, 432:
		// Legacy's table has no row for third and fourth Dual Blade jobs.
	default:
		return c, domain.ErrForbidden
	}
	if c.VitalsModel == 1 {
		c.BaseMaxHP = min(domain.MaxVital(), c.BaseMaxHP+hpGain)
		c.BaseMaxMP = min(domain.MaxVital(), c.BaseMaxMP+mpGain)
		if err := recomputeVitals(&c); err != nil {
			return c, err
		}
	} else {
		c.MaxHP = min(domain.MaxVital(), c.MaxHP+hpGain)
		c.MaxMP = min(domain.MaxVital(), c.MaxMP+mpGain)
	}
	c.HP, c.MP = c.MaxHP, c.MaxMP
	return c, nil
}

func grantFreshJobBundle(ctx context.Context, tx *sql.Tx, result *domain.InventoryResult, reward firstJobReward) error {
	definition, err := inventoryDefinition(reward.id)
	if err != nil || definition.InvType != 2 || definition.Cash || reward.count < 1 || reward.count > definition.SlotMax {
		return domain.ErrInvalidInput
	}
	used := map[int]bool{}
	for _, item := range result.Character.Items {
		if item.InvType == 2 {
			used[item.Slot] = true
		}
	}
	slot := 1
	for used[slot] && slot <= result.Character.Capacity(2) {
		slot++
	}
	if slot > result.Character.Capacity(2) {
		return domain.ErrInsufficient
	}
	item := domain.InventoryItem{TemplateID: reward.id, InvType: 2, Slot: slot, Quantity: reward.count, Revision: 1}
	if err = tx.QueryRowContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,2,$2,$3,$4) RETURNING id`, result.Character.ID, item.Slot, item.TemplateID, item.Quantity).Scan(&item.ID); err != nil {
		return err
	}
	result.Character.Items = append(result.Character.Items, item)
	result.Changes = append(result.Changes, domain.InventoryChange{Mode: 0, InvType: 2, Slot: item.Slot, Item: &item})
	return nil
}

func (s *Store) AdvanceFirstJob(ctx context.Context, r domain.JobAdvancementRequest) (domain.JobAdvancementResult, error) {
	p, err := firstJobPolicyFor(r)
	if err != nil {
		return domain.JobAdvancementResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.JobAdvancementResult{}, err
	}
	defer tx.Rollback()
	value, err := lockInventoryCharacter(ctx, tx, r.Session)
	if err != nil {
		return domain.JobAdvancementResult{}, err
	}
	var prior []byte
	var script string
	var job int
	err = tx.QueryRowContext(ctx, `SELECT source_script,to_job,result FROM game_job_advancements WHERE character_id=$1 AND operation_id=$2`, value.ID, r.OperationID).Scan(&script, &job, &prior)
	if err == nil {
		if script != r.Script || job != r.TargetJob {
			return domain.JobAdvancementResult{}, domain.ErrForbidden
		}
		var result domain.JobAdvancementResult
		if err = json.Unmarshal(prior, &result); err != nil {
			return result, err
		}
		result.Character, result.Changes, result.Replayed = value, nil, true
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.JobAdvancementResult{}, err
	}
	if p.mapID != 0 && (value.MapID != p.mapID || r.Session.Character.MapID != p.mapID) {
		return domain.JobAdvancementResult{}, domain.ErrForbidden
	}
	if p.subJob >= 0 && value.SubJob != p.subJob {
		return domain.JobAdvancementResult{}, domain.ErrForbidden
	}
	for id, state := range p.quests {
		if findQuest(value.Quests, id).State != state {
			return domain.JobAdvancementResult{}, domain.ErrForbidden
		}
	}
	if err = carrySkillVitals(&value, r.Session); err != nil {
		return domain.JobAdvancementResult{}, err
	}
	if p.verify != 0 {
		definition, e := content.QuestDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), p.verify)
		if e != nil || len(definition.Unsupported) > 0 {
			return domain.JobAdvancementResult{}, domain.ErrForbidden
		}
		var now time.Time
		if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
			return domain.JobAdvancementResult{}, err
		}
		if err = content.QuestRequirementMet(value, findQuest(value.Quests, p.verify), definition.Complete, r.NPCID, r.Script, now); err != nil {
			return domain.JobAdvancementResult{}, err
		}
	}
	value, err = firstJobStats(value, r.OperationID, p)
	if err != nil {
		return domain.JobAdvancementResult{}, err
	}
	for _, inv := range p.expand {
		if err = ExpandInventorySlotsSQL(ctx, tx, &value, inv, 4); err != nil {
			return domain.JobAdvancementResult{}, err
		}
	}
	if err = grantJobSkills(ctx, tx, &value, p.skills); err != nil {
		return domain.JobAdvancementResult{}, err
	}
	result := domain.JobAdvancementResult{InventoryResult: domain.InventoryResult{Character: value}, FromJob: p.fromJob, ToJob: p.job}
	for _, item := range p.consume {
		if err = applyQuestItem(ctx, tx, &result.InventoryResult, content.QuestItem{ID: item.id, Count: -item.count}); err != nil {
			return domain.JobAdvancementResult{}, err
		}
	}
	for _, reward := range p.rewards {
		if reward.fresh {
			err = grantFreshJobBundle(ctx, tx, &result.InventoryResult, reward)
		} else {
			err = applyQuestItem(ctx, tx, &result.InventoryResult, content.QuestItem{ID: reward.id, Count: reward.count})
		}
		if err != nil {
			return domain.JobAdvancementResult{}, err
		}
	}
	value = result.Character
	if len(p.complete) > 0 {
		var now time.Time
		if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
			return result, err
		}
		forces := make([]domain.ScriptQuestForce, len(p.complete))
		for i, id := range p.complete {
			forces[i] = domain.ScriptQuestForce{QuestID: id, State: 2}
		}
		if result.Quests, err = applyScriptQuestForces(ctx, tx, &value, forces, now); err != nil {
			return result, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE game_characters SET job=$1,str=$2,dex=$3,int_stat=$4,luk=$5,ap=$6,sp=$7 WHERE id=$8`, value.Job, value.STR, value.DEX, value.INT, value.LUK, value.AP, value.SP, value.ID); err != nil {
		return result, err
	}
	if err = saveJobState(ctx, tx, value); err != nil {
		return result, err
	}
	if err = commitInventoryState(ctx, tx, &value); err != nil {
		return result, err
	}
	result.Character = value
	raw, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO game_job_advancements(character_id,operation_id,source_script,from_job,to_job,result) VALUES($1,$2,$3,$4,$5,$6)`, value.ID, r.OperationID, r.Script, p.fromJob, r.TargetJob, raw); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	s.roster.Invalidate(ctx, value.AccountID, value.WorldID)
	return result, nil
}
