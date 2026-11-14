package content

import (
	"strconv"
	"strings"
	"time"

	"maple-go-server/internal/domain"
)

// QuestRequirementMet applies one WZ start or completion check to an
// authoritative character. The store uses it inside the transition
// transaction; the world uses it before launching a legacy quest script.
func QuestRequirementMet(value domain.Character, state domain.QuestState, req QuestRequirement, npcID int, script string, now time.Time) error {
	if len(req.Unsupported) > 0 {
		return domain.ErrInvalidInput
	}
	if req.NPCID > 0 && req.NPCID != npcID {
		return domain.ErrForbidden
	}
	if req.Script != "" && req.Script != script {
		return domain.ErrForbidden
	}
	if value.Level < req.MinLevel || value.Level > req.MaxLevel || value.Fame < req.MinFame || value.HP < req.MinHP {
		return domain.ErrForbidden
	}
	if req.MapID >= 0 && req.MapID != value.MapID {
		return domain.ErrForbidden
	}
	if !req.Start.IsZero() && now.Before(req.Start) || !req.End.IsZero() && now.After(req.End) {
		return domain.ErrForbidden
	}
	if state.State == 2 && (req.Interval <= 0 || now.Before(state.CompletedAt.Add(time.Duration(req.Interval)*time.Minute))) {
		return domain.ErrForbidden
	}
	if len(req.InfoEx) > 0 {
		id := req.InfoQuest
		if id == 0 {
			id = state.ID
		}
		if !questInfoMet(value.QuestRecords[id], req.InfoEx) {
			return domain.ErrForbidden
		}
	}
	if req.SubJobFlags != 0 && !SubJobAdmitted(req.SubJobFlags, value.SubJob) {
		return domain.ErrForbidden
	}
	if len(req.Jobs) > 0 {
		allowed := false
		for _, job := range req.Jobs {
			if value.Job == job {
				allowed = true
			}
		}
		if !allowed {
			return domain.ErrForbidden
		}
	}
	for id, expected := range req.Quests {
		if questStateIn(value.Quests, id) != expected {
			return domain.ErrForbidden
		}
	}
	for _, item := range req.Items {
		quantity := 0
		for _, owned := range value.Items {
			if owned.TemplateID == item.ID {
				quantity += owned.Quantity
			}
		}
		for _, owned := range value.Equipment {
			if owned.ItemID == item.ID {
				quantity++
			}
		}
		if quantity < item.Count || (item.Count <= 0 && quantity > 0) {
			return domain.ErrInsufficient
		}
	}
	for id, count := range req.Mobs {
		if state.MobKills[id] < count {
			return domain.ErrForbidden
		}
	}
	return nil
}

// SubJobAdmitted reports whether a subJobFlags mask admits a character's
// Explorer subcategory. The v148 data uses one bit per subcategory (4 for
// Cannoneer quests, 1024 for Jett); Legacy's "flags/2 == subcategory" reading
// agrees on 1, 2 and 4 but not on the wider masks.
func SubJobAdmitted(flags, subJob int) bool {
	return subJob >= 0 && subJob < 31 && flags&(1<<subJob) != 0
}

func questStateIn(states []domain.QuestState, id int) int {
	for _, q := range states {
		if q.ID == id {
			return q.State
		}
	}
	return 0
}

// questInfoMet is the client's infoex demand, enforced by the server as well:
// any one entry may match. The meaning of cond 1 is inferred from the WZ data,
// where its values are counts and thresholds.
func questInfoMet(record string, entries []QuestInfoCondition) bool {
	for _, entry := range entries {
		target := record
		if entry.Variable != "" {
			target = ""
			for _, pair := range strings.Split(record, ";") {
				if key, value, ok := strings.Cut(pair, "="); ok && key == entry.Variable {
					target = value
				}
			}
		}
		if entry.Cond == 0 {
			if target == entry.Value {
				return true
			}
			continue
		}
		have, e1 := strconv.Atoi(target)
		need, e2 := strconv.Atoi(entry.Value)
		if e1 == nil && e2 == nil && have >= need {
			return true
		}
	}
	return false
}
