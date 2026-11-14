package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

// applyScriptQuestForces is Legacy MapleQuest.forceStart/forceComplete: the
// status changes without WZ requirement checks or WZ rewards. Entries apply in
// order, so a script may start and complete the same quest in one command.
func applyScriptQuestForces(ctx context.Context, tx *sql.Tx, c *domain.Character, forces []domain.ScriptQuestForce, now time.Time) ([]domain.QuestState, error) {
	var out []domain.QuestState
	for _, force := range forces {
		if force.QuestID < 1 || force.QuestID > 65535 || force.State != 1 && force.State != 2 {
			return nil, domain.ErrInvalidInput
		}
		state := findQuest(c.Quests, force.QuestID)
		if force.State == 1 {
			state.State = 1
			state.StartedAt = now
			state.CompletedAt = time.Time{}
			state.MobKills = map[int]int{}
			state.MobOrder = nil
			if definition, err := content.QuestDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), force.QuestID); err == nil {
				state.MobOrder = append([]int(nil), definition.Complete.MobOrder...)
				for id := range definition.Complete.Mobs {
					state.MobKills[id] = 0
				}
			}
		} else {
			state.State = 2
			state.CompletedAt = now
			if state.MobKills == nil {
				state.MobKills = map[int]int{}
			}
		}
		state.Revision++
		raw, err := json.Marshal(state.MobKills)
		if err != nil {
			return nil, err
		}
		var started, completed any
		if !state.StartedAt.IsZero() {
			started = state.StartedAt
		}
		if !state.CompletedAt.IsZero() {
			completed = state.CompletedAt
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO game_quests(character_id,quest_id,state,started_at,completed_at,mob_kills,revision) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(character_id,quest_id) DO UPDATE SET state=$3,started_at=$4,completed_at=$5,mob_kills=$6,revision=$7`, c.ID, state.ID, state.State, started, completed, raw, state.Revision); err != nil {
			return nil, err
		}
		if force.Data != nil {
			change := []domain.ScriptQuestChange{{QuestID: force.QuestID, Data: *force.Data}}
			if _, err = applyScriptQuestData(ctx, tx, c.ID, change); err != nil {
				return nil, err
			}
			setQuestRecords(c, change)
		}
		updated := false
		for i := range c.Quests {
			if c.Quests[i].ID == state.ID {
				c.Quests[i] = state
				updated = true
			}
		}
		if !updated {
			c.Quests = append(c.Quests, state)
		}
		replaced := false
		for i := range out {
			if out[i].ID == state.ID {
				out[i] = state
				replaced = true
			}
		}
		if !replaced {
			out = append(out, state)
		}
	}
	return out, nil
}

// setQuestRecords mirrors committed custom data into the character snapshot.
// The map is copied first: the caller's session may share the old one.
func setQuestRecords(c *domain.Character, changes []domain.ScriptQuestChange) {
	if len(changes) == 0 {
		return
	}
	records := make(map[int]string, len(c.QuestRecords)+len(changes))
	for id, data := range c.QuestRecords {
		records[id] = data
	}
	for _, change := range changes {
		if change.Data == "" {
			delete(records, change.QuestID)
		} else {
			records[change.QuestID] = change.Data
		}
	}
	c.QuestRecords = records
}
