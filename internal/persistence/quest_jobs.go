package persistence

import (
	"context"
	"database/sql"

	"maple-go-server/internal/domain"
)

// questJob is a first advancement a beginner family receives by finishing a
// quest, ported from the Legacy quest script that called changeJob. The quest
// source names the job; everything else (items, bag growth, stat reset,
// skills and the quest it completes) is fixed here.
type questJob struct {
	script  string
	npc     int
	from    int
	to      int
	subJob  int
	expand  []int
	base    [4]int
	rewards []firstJobReward
	// quests must hold these states first; complete are finished with it.
	quests   map[int]int
	complete []int
	skills   []domain.SkillState
	// minLevel is the advancement level (10 when zero). verify checks the
	// quest's own WZ completion demand (items, monsters) in the transaction,
	// and consume takes the items that demand names.
	minLevel int
	verify   int
	// raiseLevel lifts a lower character to this level first (the Phantom
	// tutorial ends at level 10 whatever was fought on the way).
	raiseLevel int
	consume    []firstJobReward
}

// PhantomSceneNPC is the butler the Phantom tutorial scenes run under.
const PhantomSceneNPC = 1402000

var questJobs = []questJob{
	// Cannoneer: quest 2570 ends at Kyrin (q2570e). The cannon is the only gift.
	{script: "quest/2570.lua", npc: 1090000, from: 0, to: 501, subJob: domain.SubJobCannoneer, expand: []int{1, 2, 4},
		rewards: []firstJobReward{{1532000, 1, false}}, quests: map[int]int{2570: 1}, complete: []int{2570}},
	// Knights of Cygnus: quests 20101-20105 end at their Knight instructors.
	{script: "quest/20101.lua", npc: 1101003, from: 1000, to: 1100, subJob: -1, expand: []int{1, 4},
		rewards: []firstJobReward{{1302077, 1, false}, {1142066, 1, false}}, quests: map[int]int{20101: 1}, complete: []int{20101}},
	{script: "quest/20102.lua", npc: 1101004, from: 1000, to: 1200, subJob: -1, expand: []int{1, 4},
		rewards: []firstJobReward{{1372043, 1, false}, {1142066, 1, false}}, quests: map[int]int{20102: 1}, complete: []int{20102}},
	{script: "quest/20103.lua", npc: 1101005, from: 1000, to: 1300, subJob: -1, expand: []int{1, 4},
		rewards: []firstJobReward{{2060000, 2000, false}, {1452051, 1, false}, {1142066, 1, false}}, quests: map[int]int{20103: 1}, complete: []int{20103}},
	{script: "quest/20104.lua", npc: 1101006, from: 1000, to: 1400, subJob: -1, expand: []int{1, 4},
		rewards: []firstJobReward{{1472061, 1, false}, {2070015, 800, true}, {2070015, 800, true}, {1142066, 1, false}}, quests: map[int]int{20104: 1}, complete: []int{20104}},
	{script: "quest/20105.lua", npc: 1101007, from: 1000, to: 1500, subJob: -1, expand: []int{1, 4},
		rewards: []firstJobReward{{1482014, 1, false}, {1142066, 1, false}}, quests: map[int]int{20105: 1}, complete: []int{20105}},
	// Aran: q21101s at the polearm (1201001) after 21100; STR 35 and the medal
	// quest 29924, as in Legacy.
	{script: "quest/21101.lua", npc: 1201001, from: 2000, to: 2100, subJob: -1, expand: []int{1, 2, 3, 4}, base: [4]int{35, 4, 4, 4},
		rewards: []firstJobReward{{1142129, 1, false}}, quests: map[int]int{21100: 2, 21101: 0}, complete: []int{21101, 29924}},
	// Resistance: quests 23011-23013 end at the three hidden instructors.
	{script: "quest/23011.lua", npc: 2151001, from: 3000, to: 3200, subJob: -1, expand: []int{1, 2, 4},
		rewards: []firstJobReward{{1382100, 1, false}}, quests: map[int]int{23011: 1}, complete: []int{23011}},
	{script: "quest/23012.lua", npc: 2151002, from: 3000, to: 3300, subJob: -1, expand: []int{1, 2, 4},
		rewards: []firstJobReward{{1462092, 1, false}}, quests: map[int]int{23012: 1}, complete: []int{23012},
		skills: []domain.SkillState{{ID: 30001061, Level: 1}, {ID: 30001062, Level: 1}}},
	{script: "quest/23013.lua", npc: 2151004, from: 3000, to: 3500, subJob: -1, expand: []int{1, 2, 4},
		rewards: []firstJobReward{{1492014, 1, false}}, quests: map[int]int{23013: 1}, complete: []int{23013},
		skills: []domain.SkillState{{ID: 30001068, Level: 1}}},
	// Phantom: the tutorial ends in the Lumiere (map 150000000, PTjob1), where
	// the beginner 2003 becomes level 10 and takes job 2400 with quest 25000.
	{script: "scene/PTjob1.lua", npc: PhantomSceneNPC, from: 2003, to: 2400, subJob: -1, raiseLevel: 10,
		quests: map[int]int{25000: 1}, complete: []int{25000}},
	// Dual Blade, v148 line at Lady Syl (1056000). The rewards follow the Legacy
	// advancements of the older line: q2363e (Blade Recruit, katara 1342000 and
	// four more slots in every bag), q2369e (Blade Acolyte, nothing) and q2374e
	// (Blade Specialist, 1132021). Legacy also reset stats to 4/25/4/4 at 430;
	// a second job keeps the player's stats, as the v148 client expects.
	{script: "quest/2623.lua", npc: 1056000, from: 400, to: 430, subJob: domain.SubJobDualBlade, minLevel: 20, expand: []int{1, 2, 3, 4},
		rewards: []firstJobReward{{1342000, 1, false}}, consume: []firstJobReward{{4032616, 1, false}},
		quests: map[int]int{2622: 2, 2623: 1}, complete: []int{2623}, verify: 2623},
	{script: "quest/2638.lua", npc: 1056000, from: 430, to: 431, subJob: domain.SubJobDualBlade, minLevel: 30,
		consume: []firstJobReward{{4031013, 30, false}}, quests: map[int]int{2637: 2, 2638: 1}, complete: []int{2638}, verify: 2638},
	{script: "quest/2642.lua", npc: 1056000, from: 431, to: 432, subJob: domain.SubJobDualBlade, minLevel: 55,
		rewards: []firstJobReward{{1132021, 1, false}}, quests: map[int]int{2641: 2, 2642: 1}, complete: []int{2642}, verify: 2642},
}

func questJobPolicyFor(r domain.JobAdvancementRequest) (firstJobPolicy, error) {
	for _, q := range questJobs {
		if q.script != r.Script || q.npc != r.NPCID || q.to != r.TargetJob {
			continue
		}
		minLevel := q.minLevel
		if minLevel == 0 {
			minLevel = 10
		}
		return firstJobPolicy{job: q.to, fromJob: q.from, minLevel: minLevel, subJob: q.subJob, expand: q.expand, base: q.base,
			rewards: q.rewards, quests: q.quests, complete: q.complete, skills: q.skills, verify: q.verify, consume: q.consume, raiseLevel: q.raiseLevel}, nil
	}
	return firstJobPolicy{}, domain.ErrForbidden
}

// grantJobSkills is Legacy teachSkill(id, level, 0) for the beginner skills a
// job hands out; an existing higher level is kept.
func grantJobSkills(ctx context.Context, tx *sql.Tx, c *domain.Character, grants []domain.SkillState) error {
	for _, grant := range grants {
		state := grant
		for _, owned := range c.Skills {
			if owned.ID == grant.ID {
				state = owned
				state.Level = max(owned.Level, grant.Level)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_skills(character_id,skill_id,level,master_level) VALUES($1,$2,$3,$4) ON CONFLICT(character_id,skill_id) DO UPDATE SET level=GREATEST(game_skills.level,EXCLUDED.level)`, c.ID, state.ID, state.Level, state.MasterLevel); err != nil {
			return err
		}
		replaced := false
		for i := range c.Skills {
			if c.Skills[i].ID == state.ID {
				c.Skills[i], replaced = state, true
			}
		}
		if !replaced {
			c.Skills = append(c.Skills, state)
		}
	}
	return nil
}
