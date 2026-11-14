package world

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func recoveryEvidenceFixture(t *testing.T) (*Server, domain.CharacterSession, protocol.Regeneration, domain.Character, domain.Character, *bytes.Buffer) {
	t.Helper()
	t.Setenv("MAPLEGO_COMBAT_EVIDENCE_CHARACTERS", "7")
	var output bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	w := protocol.NewRawWriter()
	w.U32(99)
	w.I64(0x1400)
	w.I16(20)
	w.I16(10)
	q, err := protocol.ReadRegenerationOpcode(protocol.RecvPotentialRegeneration, protocol.NewReader(w.Packet()))
	if err != nil {
		t.Fatal(err)
	}
	before := domain.Character{ID: 7, AccountID: 5, WorldID: 0, MapID: 180000001, HP: 95, MaxHP: 100, MP: 20, MaxMP: 50}
	after := before
	after.HP, after.MP = 100, 24
	session := domain.CharacterSession{CharacterID: 7, AccountID: 5, SessionID: "private-recovery-session", Generation: 3, ChannelID: 5, Character: before, Lease: domain.WorldLease{WorldID: 0}}
	return new(Server), session, q, before, after, &output
}

func TestRegenerationEvidenceBindsOriginalBodyAndActualClampedGain(t *testing.T) {
	s, session, q, before, after, output := recoveryEvidenceFixture(t)
	s.recordRegenerationEvidence(session, q, before, after, 12, 4)
	var row map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &row); err != nil {
		t.Fatal(err)
	}
	if row["msg"] != "regeneration_applied" || row["proof"] != "actual_server_vitals_mutation" || row["hp_recovered"] != float64(5) ||
		row["mp_recovered"] != float64(4) || row["wire_body_sha256"] != combatEvidenceHash(string(q.Wire)) || row["durable_checkpoint_verified"] != false ||
		strings.Contains(output.String(), session.SessionID) {
		t.Fatal("recovery evidence invented healing/durability or leaked a session", row)
	}
}

func TestRegenerationEvidenceCannotBeProducedByAnUnboundOrUnappliedClaim(t *testing.T) {
	for _, which := range []string{"disabled", "wrong-actor", "changed-wire", "changed-claim", "wrong-after", "no-gain", "dead"} {
		t.Run(which, func(t *testing.T) {
			s, session, q, before, after, output := recoveryEvidenceFixture(t)
			switch which {
			case "disabled":
				t.Setenv("MAPLEGO_COMBAT_EVIDENCE_CHARACTERS", "70,0,bad")
			case "wrong-actor":
				session.CharacterID++
			case "changed-wire":
				q.Wire[4] ^= 1
			case "changed-claim":
				q.HP++
			case "wrong-after":
				after.HP++
			case "no-gain":
				before.HP, before.MP = 100, 50
				after = before
			case "dead":
				before.HP = 0
			}
			s.recordRegenerationEvidence(session, q, before, after, 12, 4)
			if output.Len() != 0 {
				t.Fatal("an unbound or unapplied claim became healing evidence", output.String())
			}
		})
	}
}

func TestRegenerationEvidenceSeparatesExpiredMaximumClampFromRecovery(t *testing.T) {
	s, session, q, before, after, output := recoveryEvidenceFixture(t)
	before.HP, before.MaxHP = 150, 160
	s.recordRegenerationEvidence(session, q, before, after, 12, 4)
	var row map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &row); err != nil || row["hp_expiry_clamp"] != float64(50) || row["hp_recovered"] != float64(0) || row["mp_recovered"] != float64(4) {
		t.Fatal("expired maximum was confused with healing", row, err)
	}
}
