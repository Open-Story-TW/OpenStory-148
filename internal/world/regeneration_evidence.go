package world

import (
	"bytes"
	"log/slog"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// The caller has applied this recovery under persistMu and the exact visit's
// publication gate. This opt-in record proves the runtime HP/MP change; it
// does not turn a dirty checkpoint into a durable economic transaction.
func (s *Server) recordRegenerationEvidence(session domain.CharacterSession, q protocol.Regeneration, before, after domain.Character, capHP, capMP int) {
	if !combatEvidenceEnabled(session.CharacterID) || session.CharacterID <= 0 ||
		before.ID != session.CharacterID || after.ID != before.ID || before.AccountID != session.AccountID || after.AccountID != before.AccountID ||
		before.WorldID != session.Lease.WorldID || after.WorldID != before.WorldID || before.MapID != after.MapID || after.MapID != session.Character.MapID ||
		before.HP <= 0 || after.MaxHP < 1 || after.MaxMP < 0 || capHP < 0 || capMP < 0 {
		return
	}
	parsed, err := protocol.ReadRegenerationOpcode(q.Opcode, protocol.NewReader(q.Wire))
	if err != nil || parsed.Tick != q.Tick || parsed.HP != q.HP || parsed.MP != q.MP || parsed.Mode != q.Mode || !bytes.Equal(parsed.Wire, q.Wire) {
		return
	}
	// Timed maximum buffs may expire before this request is applied. Keep the
	// clamp distinct from recovery so a falling HP value cannot be called healing.
	baseHP, baseMP := min(before.HP, after.MaxHP), min(before.MP, after.MaxMP)
	if baseMP < 0 || after.HP != min(after.MaxHP, baseHP+min(q.HP, capHP)) ||
		after.MP != min(after.MaxMP, baseMP+min(q.MP, capMP)) {
		return
	}
	healedHP, healedMP := after.HP-baseHP, after.MP-baseMP
	if healedHP <= 0 && healedMP <= 0 {
		return
	}
	source := "natural"
	if q.Opcode == protocol.RecvPotentialRegeneration {
		source = "potential"
	}
	slog.Info("regeneration_applied", "character", session.CharacterID,
		"world", session.Lease.WorldID, "channel", session.ChannelID, "map", after.MapID,
		"generation", session.Generation, "source", source, "opcode", q.Opcode, "mode", q.Mode,
		"client_tick", q.Tick, "wire_body_sha256", combatEvidenceHash(string(q.Wire)),
		"claimed_hp", q.HP, "claimed_mp", q.MP, "cap_hp", capHP, "cap_mp", capMP,
		"hp_before", before.HP, "mp_before", before.MP, "max_hp", after.MaxHP, "max_mp", after.MaxMP,
		"hp_expiry_clamp", before.HP-baseHP, "mp_expiry_clamp", before.MP-baseMP,
		"hp_after", after.HP, "mp_after", after.MP, "hp_recovered", healedHP, "mp_recovered", healedMP,
		"proof", "actual_server_vitals_mutation", "durable_checkpoint_verified", false)
}
