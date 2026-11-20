package world

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"maple-go-server/internal/domain"
)

// Acceptance tracing is opt-in for exact QA character IDs. Capture follows the
// real HP write under gameplay.mu; client numbers and a submitted transaction
// alone cannot produce this evidence. Socket/log IO stays outside the map lock.
type appliedDamageEvidence struct {
	ObjectID          int32    `json:"object_id"`
	MonsterID         int      `json:"monster_id"`
	SpawnHash         string   `json:"spawn_hash"`
	BeforeHP          int64    `json:"hp_before"`
	AfterHP           int64    `json:"hp_after"`
	Applied           int64    `json:"hp_removed"`
	Defeated          bool     `json:"defeated"`
	DurableSettlement bool     `json:"durable_settlement"`
	ClaimedLines      []uint32 `json:"claimed_lines_raw"`
	// Long tokens are aliases on the wire. Keep the numbers the server resolved
	// before applying skill limits so acceptance can compare the full values.
	ClaimedValues []int64 `json:"claimed_lines_value,omitempty"`
	// These are the accepted line values, which may already include ClipToHP.
	// They do not prove separate HP writes for each displayed damage line.
	BoundedLines    []int                      `json:"bounded_lines"`
	LineCriticality []domain.CombatCriticality `json:"line_criticality,omitempty"`
}

func combatEvidenceEnabled(characterID int64) bool {
	for _, part := range strings.Split(os.Getenv("MAPLEGO_COMBAT_EVIDENCE_CHARACTERS"), ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err == nil && id > 0 && id == characterID {
			return true
		}
	}
	return false
}
func combatEvidenceHash(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}
func (t *combatTicket) recordAppliedDamageLocked(q domain.CombatTarget, actualAfterHP int64, durable bool) {
	if !combatEvidenceEnabled(t.request.Session.CharacterID) {
		return
	}
	e := appliedDamageEvidence{ObjectID: q.ObjectID, MonsterID: q.MobID,
		SpawnHash: combatEvidenceHash(q.SpawnOperationID), BeforeHP: q.BeforeHP,
		AfterHP: actualAfterHP, Applied: q.BeforeHP - actualAfterHP,
		Defeated: actualAfterHP == 0, DurableSettlement: durable}
	hasCriticality, hasLong := false, false
	for _, line := range q.Lines {
		e.ClaimedLines = append(e.ClaimedLines, line.Raw)
		e.ClaimedValues = append(e.ClaimedValues, line.Claimed())
		e.BoundedLines = append(e.BoundedLines, line.Applied)
		e.LineCriticality = append(e.LineCriticality, line.Criticality)
		hasCriticality = hasCriticality || line.Criticality != ""
		hasLong = hasLong || line.Long != 0
	}
	if !hasLong {
		e.ClaimedValues = nil
	}
	if !hasCriticality {
		e.LineCriticality = nil
	}
	t.damageEvidence = append(t.damageEvidence, e)
}
func (t *combatTicket) publishAppliedDamageEvidence() {
	rows := t.damageEvidence
	t.damageEvidence = nil // resolving/publishing again cannot repeat HP evidence
	if len(rows) == 0 {
		return
	}
	clientTick := t.attack.Tick
	serverGenerated := false
	wireBody := t.attack.Wire
	if t.generic != nil {
		clientTick = t.generic.attack.Tick
		serverGenerated = t.generic.serverDamage
		wireBody = t.generic.attack.Wire
	} else if t.magic != nil {
		clientTick = t.magic.Tick
		wireBody = t.magic.Wire
	}
	var wireHash any
	if len(wireBody) > 0 {
		wireHash = combatEvidenceHash(string(wireBody))
	}
	slog.Info("combat_damage_committed", "character", t.request.Session.CharacterID,
		"world", t.request.Session.Lease.WorldID, "channel", t.key.channel, "map", t.key.mapID,
		"skill", t.request.SkillID, "family", t.request.Family, "settlement_tick", t.request.ClientTick,
		"client_tick", clientTick, "server_generated", serverGenerated,
		"wire_body_sha256", wireHash,
		"operation_hash", combatEvidenceHash(t.request.OperationID), "targets", rows,
		"proof", "actual_server_hp_mutation", "per_line_hp_removal_verified", false)
}
