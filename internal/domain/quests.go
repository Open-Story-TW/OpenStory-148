package domain

import (
	"context"
	"time"
)

type QuestState struct {
	ID          int         `json:"id"`
	State       int         `json:"state"` // 0=not started/forfeited, 1=started, 2=completed.
	StartedAt   time.Time   `json:"started_at,omitempty"`
	CompletedAt time.Time   `json:"completed_at,omitempty"`
	MobKills    map[int]int `json:"mob_kills,omitempty"`
	MobOrder    []int       `json:"mob_order,omitempty"`
	Revision    int64       `json:"revision"`
}

type QuestRequest struct {
	Session     CharacterSession
	QuestID     int
	NPCID       int
	Action      string
	OperationID string
	Script      string // authenticated Lua source name; never copied from client input.
	Selection   int
}

type QuestResult struct {
	InventoryResult
	Quest     QuestState `json:"quest"`
	NextQuest int        `json:"next_quest,omitempty"`
	// Binds retries to the same player-selected reward. Nil means this
	// transition did not offer a selectable reward (also true of old receipts).
	RewardSelection *int `json:"reward_selection,omitempty"`
}

type QuestRepository interface {
	QuestStates(context.Context, CharacterSession) ([]QuestState, error)
	TransitionQuest(context.Context, QuestRequest) (QuestResult, error)
}
