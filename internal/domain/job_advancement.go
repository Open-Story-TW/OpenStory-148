package domain

import "context"

// JobAdvancementRequest is constructed by the bound NPC host. TargetJob is a
// reviewed source/menu choice validated against NPC and script by the store;
// stat totals, bonuses, rewards, and inventory sizes are never client supplied.
type JobAdvancementRequest struct {
	Session     CharacterSession
	OperationID string
	NPCID       int
	Script      string
	TargetJob   int
}

type JobAdvancementResult struct {
	InventoryResult
	FromJob int
	ToJob   int
	// Quests are the quest states a quest-bound advancement finished in the
	// same transaction.
	Quests []QuestState `json:"quests,omitempty"`
}

type JobAdvancementRepository interface {
	AdvanceWarrior(context.Context, JobAdvancementRequest) (JobAdvancementResult, error)
}

type FirstJobAdvancementRepository interface {
	AdvanceFirstJob(context.Context, JobAdvancementRequest) (JobAdvancementResult, error)
}
