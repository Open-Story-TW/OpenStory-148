package domain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

type CombatFamily uint8

const (
	CombatNormal CombatFamily = iota
	CombatThreeSnails
	CombatPhysicalSkill
	// CombatSkill is the S0b generic pipeline for registered skills and
	// non-beginner ordinary attacks. Cost and per-line damage are planned by
	// internal/combat under the room lock and bounded again in persistence.
	CombatSkill
)

// CombatCost is paid in the settlement transaction.
type CombatCost struct {
	Opcode     uint16 `json:",omitempty"` // Actual attack family, revalidated against the registered spec.
	MP, HP     int    `json:",omitempty"`
	AmmoItemID int    `json:",omitempty"`
	AmmoSlot   int    `json:",omitempty"`
	Ammo       int    `json:",omitempty"`
	Level      int    `json:",omitempty"` // Learned skill level authorizing the cost.
	CooldownMS int    `json:",omitempty"`
	Summon     int32  `json:",omitempty"` // Summon object ID for summon attacks (no cost).
	HPGain     int    `json:",omitempty"` // Drain (lifesteal) bounded by dealt damage.
	Meso       int    `json:",omitempty"` // moneyCon paid per attack.
	MPGain     int    `json:",omitempty"`
	// Ongoing marks an attack of an active cast (keydown body attack or the
	// skill's own running buff): the cast's cooldown neither blocks it nor
	// restarts (CooldownMS must be 0).
	Ongoing bool `json:",omitempty"`
}

// CombatAmmoResult is the post-commit projectile stack.
type CombatAmmoResult struct {
	Slot, ItemID, Quantity int
}

// CombatContribution records effective damage actually removed from a live
// spawn. A reconnect cannot inherit another generation's contribution.
type CombatContribution struct {
	CharacterID int64
	SessionID   string
	Generation  int64
	Damage      int64
}

type CombatDamageLine struct {
	Raw         uint32
	Applied     int
	Criticality CombatCriticality `json:",omitempty"`
	Long        int64             `json:",omitempty"` // Value a long damage token stood for.
}

// Claimed is the damage this line asked for, without the critical bit.
func (l CombatDamageLine) Claimed() int64 {
	if l.Long != 0 {
		return max(l.Long, 0)
	}
	return int64(l.Raw & 0x7fffffff)
}

// Criticality describes evidence in the native wire, not a server-side roll.
// Unknown preserves that reviewed packets can carry critical damage without
// setting bit31; it must never be rewritten into an asserted critical flag.
type CombatCriticality string

const (
	CombatCriticalUnknown  CombatCriticality = "unknown"
	CombatCriticalExplicit CombatCriticality = "explicit_wire_bit"
)

type CombatTarget struct {
	ObjectID         int32
	MobID            int
	SpawnOperationID string
	X, Y             int
	BeforeHP         int64
	Damage           int
	Defeated         bool
	Contributions    []CombatContribution // Inclusive of this attack only on successful commit.
	Lines            []CombatDamageLine   `json:",omitempty"`
}

// CombatPartyPin is a short-lived membership/revision snapshot. The settlement
// transaction checks it again under the same world party lock as party commands.
type CombatPartyPin struct {
	ID, Revision int64
	Members      []int64
}

type CombatAttackRequest struct {
	Session        CharacterSession
	OperationID    string
	ClientTick     uint32
	Family         CombatFamily
	SkillID        int
	Targets        []CombatTarget
	Members        []CharacterSession // Current live participants locked by CharacterID.
	Parties        []CombatPartyPin
	LinesPerTarget int         `json:",omitempty"`
	Wire           []byte      `json:",omitempty"` // Native attack-only body, never credentials/dialogue.
	Cost           *CombatCost `json:",omitempty"`
	// MesoDrops are the ground mesos a Meso Explosion consumes; they are
	// retired inside the settlement transaction (nothing is lost on reject).
	MesoDrops []int32 `json:",omitempty"`
}

type CombatGrant struct {
	CharacterID                   int64
	BaseEXP, PartyEXP, Experience int64
	PartySize                     int
	QuestKill                     bool
}

type CombatSpawnResult struct {
	SpawnOperationID string
	MobID            int
	Grants           []CombatGrant
}

type CombatAttackResult struct {
	OperationID, ManifestHash string
	Characters                []Character
	Spawns                    []CombatSpawnResult
	Replayed                  bool
	Ammo                      *CombatAmmoResult `json:",omitempty"`
}

// CombatSettlementRepository is shared by every implemented attack family.
// LookupOutcome resolves an uncertain commit without another kill or reroll.
type CombatSettlementRepository interface {
	CombatParties(context.Context, WorldLease, []int64) ([]CombatPartyPin, error)
	ApplyCombatAttack(context.Context, CombatAttackRequest) (CombatAttackResult, error)
	CombatAttackOutcome(context.Context, WorldLease, string, string) (*CombatAttackResult, error)
}

// CombatManifestHash binds effect identity, damage ledger and membership, not
// later movement/HP snapshots. Retries cannot substitute another recipient or
// target while the same committed command remains recognizable after a save.
func CombatManifestHash(r CombatAttackRequest) (string, error) {
	type identity struct {
		ID                  int64
		Session             string
		Generation          int64
		World, Channel, Map int
	}
	q := struct {
		Actor          identity
		Operation      string
		Tick           uint32
		Family         CombatFamily
		Skill          int
		Targets        []CombatTarget
		Members        []identity
		Parties        []CombatPartyPin
		LinesPerTarget int         `json:",omitempty"`
		Wire           []byte      `json:",omitempty"`
		Cost           *CombatCost `json:",omitempty"`
	}{Operation: r.OperationID, Tick: r.ClientTick, Family: r.Family, Skill: r.SkillID}
	id := func(s CharacterSession) identity {
		return identity{s.CharacterID, s.SessionID, s.Generation, s.Lease.WorldID, s.ChannelID, s.Character.MapID}
	}
	q.Actor = id(r.Session)
	q.LinesPerTarget = r.LinesPerTarget
	q.Wire = append([]byte(nil), r.Wire...)
	q.Cost = r.Cost
	q.Targets = append([]CombatTarget(nil), r.Targets...)
	for i := range q.Targets {
		q.Targets[i].Lines = append([]CombatDamageLine(nil), r.Targets[i].Lines...)
		q.Targets[i].Contributions = append([]CombatContribution(nil), q.Targets[i].Contributions...)
		sort.Slice(q.Targets[i].Contributions, func(a, b int) bool {
			x, y := q.Targets[i].Contributions[a], q.Targets[i].Contributions[b]
			if x.CharacterID != y.CharacterID {
				return x.CharacterID < y.CharacterID
			}
			if x.Generation != y.Generation {
				return x.Generation < y.Generation
			}
			return x.SessionID < y.SessionID
		})
	}
	sort.Slice(q.Targets, func(i, j int) bool { return q.Targets[i].ObjectID < q.Targets[j].ObjectID })
	for _, m := range r.Members {
		q.Members = append(q.Members, id(m))
	}
	sort.Slice(q.Members, func(i, j int) bool { return q.Members[i].ID < q.Members[j].ID })
	for _, p := range r.Parties {
		p.Members = append([]int64(nil), p.Members...)
		sort.Slice(p.Members, func(i, j int) bool { return p.Members[i] < p.Members[j] })
		q.Parties = append(q.Parties, p)
	}
	sort.Slice(q.Parties, func(i, j int) bool { return q.Parties[i].ID < q.Parties[j].ID })
	raw, err := json.Marshal(q)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:]), nil
}
