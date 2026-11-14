package domain

import "time"

const (
	QAMobMap      = 180000001
	QAMobMaxBatch = 12
	QAMobLifetime = 15 * time.Minute
)

// QAMobAuthorization is an audited, short-lived runtime capability. Monsters
// remain ordinary field objects: this is not an inventory or monster DB row.
// RuntimeID prevents a receipt from a previous world process creating them again.
type QAMobAuthorization struct {
	RuntimeID     string    `json:"runtime_id"`
	ExpiresAt     time.Time `json:"expires_at"`
	GrantRevision int64     `json:"grant_revision"`
}

func ValidQAMobCommand(name string, args []int64, mapID int) bool {
	if mapID != QAMobMap {
		return false
	}
	switch name {
	case "spawn":
		return (len(args) == 2 || len(args) == 3 && args[2] >= -32000 && args[2] <= 32000) && args[0] > 0 && args[0] <= 9999999 && args[1] >= 1 && args[1] <= QAMobMaxBatch
	case "clearmobs":
		return len(args) == 0
	}
	return false
}
