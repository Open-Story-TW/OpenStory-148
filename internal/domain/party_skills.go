package domain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// Members are frozen server-authoritative candidate sessions, never a wire list.
// Party identity is pinned before acquiring sorted live persistence locks and
// revalidated in the same SQL transaction as every recipient's effect/cost.
type PartySkillCastRequest struct {
	Session                CharacterSession
	OperationID            string
	ClientTick             uint32
	SkillID, ClientLevel   int
	PartyID, PartyRevision int64
	PartyMembers           []int64
	Members                []CharacterSession
}
type PartySkillRecipient struct {
	Session        CharacterSession
	Character      Character
	Expired        []SkillBuff
	Inventory      []InventoryChange // The caster's consumed items.
	ResetCooldowns []SkillCooldown
	Updated        []SkillBuff // The caster's retimed rows.
}
type PartySkillCastResult struct {
	OperationID, ManifestHash string
	Recipients                []PartySkillRecipient
	Replayed                  bool
	// ToggledOff is a Toggle recast that removed the skill's buff from the
	// caster and the party instead of applying it.
	ToggledOff bool
}
type PartySkillRepository interface {
	ApplyPartySkillCast(context.Context, PartySkillCastRequest) (PartySkillCastResult, error)
	// Same operation/advisory lock only; nil proves original transaction ended
	// without commit. No policy re-execution or new random/recipient selection.
	PartySkillCastOutcome(context.Context, WorldLease, string, string) (*PartySkillCastResult, error)
}

func PartySkillManifestHash(r PartySkillCastRequest) (string, error) {
	r.PartyMembers = append([]int64(nil), r.PartyMembers...)
	sort.Slice(r.PartyMembers, func(i, j int) bool { return r.PartyMembers[i] < r.PartyMembers[j] })
	r.Members = append([]CharacterSession(nil), r.Members...)
	sort.Slice(r.Members, func(i, j int) bool { return r.Members[i].CharacterID < r.Members[j].CharacterID })
	b, e := json.Marshal(r)
	if e != nil {
		return "", e
	}
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:]), nil
}
