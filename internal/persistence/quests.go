package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

var _ domain.QuestRepository = (*Store)(nil)

type questQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readQuestStates(ctx context.Context, q questQuerier, characterID int64) ([]domain.QuestState, error) {
	rows, err := q.QueryContext(ctx, `SELECT quest_id,state,started_at,completed_at,mob_kills,revision FROM game_quests WHERE character_id=$1 ORDER BY quest_id`, characterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := make([]domain.QuestState, 0)
	for rows.Next() {
		var state domain.QuestState
		var started, completed sql.NullTime
		var raw []byte
		if err = rows.Scan(&state.ID, &state.State, &started, &completed, &raw, &state.Revision); err != nil {
			return nil, err
		}
		state.StartedAt = started.Time
		state.CompletedAt = completed.Time
		if err = json.Unmarshal(raw, &state.MobKills); err != nil {
			return nil, err
		}
		if definition, e := content.QuestDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), state.ID); e == nil {
			state.MobOrder = append([]int(nil), definition.Complete.MobOrder...)
		}
		states = append(states, state)
	}
	return states, rows.Err()
}
func readQuestRecords(ctx context.Context, q questQuerier, characterID int64) (map[int]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT quest_id,data FROM game_script_quest_records WHERE character_id=$1 AND data<>''`, characterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records map[int]string
	for rows.Next() {
		var id int
		var data string
		if err = rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		if records == nil {
			records = map[int]string{}
		}
		records[id] = data
	}
	return records, rows.Err()
}
func (s *Store) QuestStates(ctx context.Context, session domain.CharacterSession) ([]domain.QuestState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	value, err := lockInventoryCharacter(ctx, tx, session)
	if err != nil {
		return nil, err
	}
	return value.Quests, nil
}
func findQuest(states []domain.QuestState, id int) domain.QuestState {
	for _, q := range states {
		if q.ID == id {
			return q
		}
	}
	return domain.QuestState{ID: id, State: 0, MobKills: map[int]int{}}
}
func questRequirements(value domain.Character, state domain.QuestState, req content.QuestRequirement, request domain.QuestRequest, now time.Time) error {
	return content.QuestRequirementMet(value, state, req, request.NPCID, request.Script, now)
}

func (s *Store) TransitionQuest(ctx context.Context, request domain.QuestRequest) (domain.QuestResult, error) {
	if request.QuestID < 1 || request.QuestID > 65535 || len(request.OperationID) < 16 || len(request.OperationID) > 128 || request.Selection < -1 || request.Selection > 65535 || request.Action != "complete" && request.Selection != 0 {
		return domain.QuestResult{}, domain.ErrInvalidInput
	}
	definition, err := content.QuestDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), request.QuestID)
	if err != nil || len(definition.Unsupported) > 0 {
		return domain.QuestResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.QuestResult{}, err
	}
	defer tx.Rollback()
	value, err := lockInventoryCharacter(ctx, tx, request.Session)
	if err != nil {
		return domain.QuestResult{}, err
	}
	var priorID, priorNPC int
	var priorAction, priorScript string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT quest_id,action,npc_id,script,result FROM asset_quest_commands WHERE character_id=$1 AND operation_id=$2`, value.ID, request.OperationID).Scan(&priorID, &priorAction, &priorNPC, &priorScript, &raw)
	if err == nil {
		if priorID != request.QuestID || priorNPC != request.NPCID || priorAction != request.Action || priorScript != request.Script {
			return domain.QuestResult{}, domain.ErrForbidden
		}
		var result domain.QuestResult
		if err = json.Unmarshal(raw, &result); err != nil {
			return result, err
		}
		if result.RewardSelection != nil && *result.RewardSelection != request.Selection {
			return domain.QuestResult{}, domain.ErrForbidden
		}
		result.Character = value
		result.Quest = findQuest(value.Quests, request.QuestID)
		result.Replayed = true
		result.Changes, result.Buffs, result.Replaced = nil, nil, nil
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.QuestResult{}, err
	}
	if value.Revision != request.Session.Character.Revision {
		return domain.QuestResult{}, domain.ErrStaleOwner
	}
	if request.Action != "forfeit" && !definition.RemoteStart(request.Action) {
		catalog, e := content.DefaultCatalog()
		if e != nil {
			return domain.QuestResult{}, e
		}
		worldMap, e := catalog.Map(value.MapID)
		if e != nil {
			return domain.QuestResult{}, domain.ErrForbidden
		}
		present := false
		for _, life := range worldMap.Life {
			if life.Kind == "n" && life.TemplateID == request.NPCID {
				present = true
				break
			}
		}
		if !present {
			return domain.QuestResult{}, domain.ErrForbidden
		}
	}
	value.HP, value.MP = request.Session.Character.HP, request.Session.Character.MP
	if value.HP <= 0 || value.HP > value.MaxHP || value.MP < 0 || value.MP > value.MaxMP {
		return domain.QuestResult{}, domain.ErrInvalidInput
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return domain.QuestResult{}, err
	}
	state := findQuest(value.Quests, request.QuestID)
	var action content.QuestAction
	switch request.Action {
	case "start":
		if state.State == 1 || state.State == 2 && definition.Start.Interval <= 0 {
			return domain.QuestResult{}, domain.ErrAlreadyExists
		}
		if err = questRequirements(value, state, definition.Start, request, now); err != nil {
			return domain.QuestResult{}, err
		}
		action = definition.OnStart
		state.State = 1
		state.StartedAt = now
		state.CompletedAt = time.Time{}
		state.MobKills = map[int]int{}
		state.MobOrder = append([]int(nil), definition.Complete.MobOrder...)
		for id := range definition.Complete.Mobs {
			state.MobKills[id] = 0
		}
	case "complete":
		if state.State != 1 {
			return domain.QuestResult{}, domain.ErrForbidden
		}
		if err = questRequirements(value, state, definition.Complete, request, now); err != nil {
			return domain.QuestResult{}, err
		}
		action = definition.OnComplete
		state.State = 2
		state.CompletedAt = now
	case "forfeit":
		if state.State != 1 || !definition.CanForfeit {
			return domain.QuestResult{}, domain.ErrForbidden
		}
		state.State = 0
		state.StartedAt = time.Time{}
		state.CompletedAt = time.Time{}
		state.MobKills = map[int]int{}
		state.MobOrder = nil
	default:
		return domain.QuestResult{}, domain.ErrInvalidInput
	}
	if len(action.Unsupported) > 0 {
		return domain.QuestResult{}, domain.ErrInvalidInput
	}
	result := domain.QuestResult{InventoryResult: domain.InventoryResult{Character: value}, Quest: state, NextQuest: action.NextQuest}
	if action.HPDelta != 0 && value.HP >= action.HPAtLeast {
		result.Character.HP = max(1, min(value.MaxHP, value.HP+action.HPDelta))
	}
	selection := -1
	if request.Action == "complete" {
		selection = request.Selection
	}
	rewardItems, err := chooseQuestItems(action.Items, result.Character, selection, randomQuestDraw)
	if err != nil {
		return domain.QuestResult{}, err
	}
	for _, item := range rewardItems {
		if item.Weight != nil && *item.Weight == -1 {
			chosen := selection
			result.RewardSelection = &chosen
			break
		}
	}
	for _, item := range rewardItems {
		if item.IfAbsent {
			present := false
			for _, owned := range result.Character.Items {
				if owned.TemplateID == item.ID {
					present = true
				}
			}
			if present {
				continue
			}
		}
		if err = applyQuestItem(ctx, tx, &result.InventoryResult, item); err != nil {
			return domain.QuestResult{}, err
		}
	}
	if action.ItemBuff != 0 {
		recovery, buff, definitionErr := content.ConsumableDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), action.ItemBuff)
		if definitionErr != nil || buff == nil {
			return domain.QuestResult{}, domain.ErrInvalidInput
		}
		result.Buffs, result.Replaced, err = applyItemBuff(ctx, tx, &result.Character, action.ItemBuff, buff, now)
		if err != nil {
			return domain.QuestResult{}, err
		}
		result.Character.HP = min(result.Character.MaxHP, result.Character.HP+recovery.HP)
	}
	result.Character.Mesos += action.Mesos
	if result.Character.Mesos < 0 || result.Character.Mesos > domain.MaxMoney() {
		return domain.QuestResult{}, domain.ErrInsufficient
	}
	if err = applyQuestExperience(&result.Character, action.EXP, request.OperationID); err != nil {
		return domain.QuestResult{}, err
	}
	if action.Fame != 0 {
		result.Character.Fame = max(-30000, min(30000, result.Character.Fame+action.Fame))
	}
	if _, err = tx.ExecContext(ctx, `UPDATE game_characters SET level=$1,exp=$2,str=$3,ap=$4,sp=$5,fame=$6 WHERE id=$7`, result.Character.Level, result.Character.EXP, result.Character.STR, result.Character.AP, result.Character.SP, result.Character.Fame, value.ID); err != nil {
		return domain.QuestResult{}, err
	}
	state.Revision++
	result.Quest = state
	raw, err = json.Marshal(state.MobKills)
	if err != nil {
		return domain.QuestResult{}, err
	}
	var started, completed any
	if !state.StartedAt.IsZero() {
		started = state.StartedAt
	}
	if !state.CompletedAt.IsZero() {
		completed = state.CompletedAt
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO game_quests(character_id,quest_id,state,started_at,completed_at,mob_kills,revision) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(character_id,quest_id) DO UPDATE SET state=$3,started_at=$4,completed_at=$5,mob_kills=$6,revision=$7`, value.ID, state.ID, state.State, started, completed, raw, state.Revision); err != nil {
		return domain.QuestResult{}, err
	}
	updated := false
	for i, q := range result.Character.Quests {
		if q.ID == state.ID {
			result.Character.Quests[i] = state
			updated = true
		}
	}
	if !updated {
		result.Character.Quests = append(result.Character.Quests, state)
	}
	if err = commitInventoryState(ctx, tx, &result.Character); err != nil {
		return domain.QuestResult{}, err
	}
	raw, err = json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_quest_commands(character_id,operation_id,quest_id,action,npc_id,script,result) VALUES($1,$2,$3,$4,$5,$6,$7)`, value.ID, request.OperationID, state.ID, request.Action, request.NPCID, request.Script, raw); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return domain.QuestResult{}, err
	}
	s.roster.Invalidate(ctx, value.AccountID, value.WorldID)
	return result, nil
}

func applyQuestExperience(c *domain.Character, exp int64, operationID string) error {
	if exp < 0 || exp > domain.MaxExperience()-c.EXP {
		return domain.ErrInvalidInput
	}
	if exp == 0 {
		return nil
	}
	hpBase, mpBase, mpRange, minimumLevel := 12, 10, 3, 1
	// MapleCharacter.levelUp. The reviewed Explorer first jobs keep their
	// advancement-level floors; every other v145 job uses levelGrowth.
	switch c.Job {
	case 0:
	case 100:
		hpBase, mpBase, minimumLevel = 48, 4, 10
	case 130:
		hpBase, mpBase, minimumLevel = 48, 4, 30
	case 200:
		hpBase, mpBase, mpRange, minimumLevel = 10, 48, 5, 8
	case 300, 400:
		hpBase, mpBase, minimumLevel = 20, 14, 10
	case 500:
		hpBase, mpBase, mpRange, minimumLevel = 20, 18, 5, 10
	default:
		var ok bool
		if hpBase, mpBase, mpRange, ok = levelGrowth(c.Job); !ok {
			return domain.ErrInvalidInput
		}
	}
	if c.Level < minimumLevel {
		return domain.ErrInvalidInput
	}
	c.EXP += exp
	seed := sha256.Sum256([]byte(operationID))
	step := 0
	for c.Level < domain.MaxCharacterLevel() && c.EXP >= content.ExperienceForLevel(c.Level) {
		c.EXP -= content.ExperienceForLevel(c.Level)
		c.Level++
		if !domain.IsBeginnerJob(c.Job) {
			c.SP += 3
		}
		hpGain, mpGain := hpBase+int(seed[step%32])%5, mpBase+int(seed[(step+1)%32])%mpRange
		if c.VitalsModel == 1 {
			c.BaseMaxHP = min(domain.MaxVital(), c.BaseMaxHP+hpGain)
			c.BaseMaxMP = min(domain.MaxVital(), c.BaseMaxMP+mpGain)
			if err := recomputeVitals(c); err != nil {
				return err
			}
		} else {
			c.MaxHP = min(domain.MaxVital(), c.MaxHP+hpGain)
			c.MaxMP = min(domain.MaxVital(), c.MaxMP+mpGain)
		}
		step += 2
		c.HP = c.MaxHP
		c.MP = c.MaxMP
		c.AP += 5
		if domain.IsCygnusJob(c.Job) && c.Level <= 70 {
			c.AP++ // Legacy levelUp: Cygnus Knights get 6 AP through 70
		}
		if c.Job == 0 && c.Level <= 10 {
			c.STR += c.AP
			c.AP = 0
		}
	}
	return nil
}

func recordQuestKill(ctx context.Context, tx *sql.Tx, value *domain.Character, operationID string, mobID int) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO game_quest_kill_receipts(character_id,operation_id,mob_id) VALUES($1,$2,$3)`, value.ID, operationID, mobID); err != nil {
		return err
	}
	for i := range value.Quests {
		state := &value.Quests[i]
		if state.State != 1 {
			continue
		}
		definition, err := content.QuestDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), state.ID)
		if err != nil {
			return err
		}
		needed := definition.Complete.Mobs[mobID]
		if needed <= 0 || state.MobKills[mobID] >= needed {
			continue
		}
		if state.MobKills == nil {
			state.MobKills = map[int]int{}
		}
		state.MobKills[mobID]++
		state.Revision++
		raw, err := json.Marshal(state.MobKills)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE game_quests SET mob_kills=$1,revision=$2 WHERE character_id=$3 AND quest_id=$4 AND state=1`, raw, state.Revision, value.ID, state.ID); err != nil {
			return err
		}
	}
	return nil
}
