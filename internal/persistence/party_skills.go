package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"os"
	"sort"
	"time"
)

var _ domain.PartySkillRepository = (*Store)(nil)

func (s *Store) PartySkillCastOutcome(ctx context.Context, lease domain.WorldLease, op, hash string) (*domain.PartySkillCastResult, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = guardLease(ctx, tx, lease); e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, op); e != nil {
		return nil, e
	}
	var actual string
	var raw []byte
	e = tx.QueryRowContext(ctx, `SELECT payload_hash,result FROM asset_skill_casts WHERE operation_id=$1 AND world_id=$2`, op, lease.WorldID).Scan(&actual, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if actual != hash {
		return nil, domain.ErrForbidden
	}
	var r domain.PartySkillCastResult
	if e = json.Unmarshal(raw, &r); e != nil {
		return nil, e
	}
	if r.OperationID != op || r.ManifestHash != hash {
		return nil, domain.ErrForbidden
	}
	r.Replayed = true
	return &r, nil
}

func validPartySkillRequest(q domain.PartySkillCastRequest) bool {
	h, ok := skills.Lookup(q.SkillID)
	if !ok || h.Cast == nil || h.Kind&skills.KindPartyBuff == 0 || q.ClientLevel < 1 || q.ClientLevel > 255 || len(q.OperationID) < 16 || len(q.OperationID) > 200 || len(q.Members) < 1 || len(q.Members) > domain.PartyCapacity || len(q.PartyMembers) > domain.PartyCapacity {
		return false
	}
	seen := map[int64]bool{}
	source := false
	for _, m := range q.Members {
		if m.CharacterID < 1 || m.Character.ID != m.CharacterID || seen[m.CharacterID] || m.Lease.WorldID != q.Session.Lease.WorldID || m.Lease.InstanceID != q.Session.Lease.InstanceID || m.Lease.Epoch != q.Session.Lease.Epoch || m.ChannelID != q.Session.ChannelID || m.Character.MapID != q.Session.Character.MapID {
			return false
		}
		seen[m.CharacterID] = true
		if m.CharacterID == q.Session.CharacterID {
			source = m.AccountID == q.Session.AccountID && m.SessionID == q.Session.SessionID && m.Generation == q.Session.Generation && m.Character.Revision == q.Session.Character.Revision
		}
	}
	return source
}

func (s *Store) ApplyPartySkillCast(ctx context.Context, q domain.PartySkillCastRequest) (domain.PartySkillCastResult, error) {
	r := domain.PartySkillCastResult{OperationID: q.OperationID}
	if !validPartySkillRequest(q) {
		return r, domain.ErrInvalidInput
	}
	hash, e := domain.PartySkillManifestHash(q)
	if e != nil {
		return r, e
	}
	r.ManifestHash = hash
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return r, e
	}
	defer tx.Rollback()
	if e = guardBuddyOwner(ctx, tx, q.Session); e != nil {
		return r, e
	}
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, q.OperationID); e != nil {
		return r, e
	}
	var oldOp, oldHash string
	var raw []byte
	e = tx.QueryRowContext(ctx, `SELECT operation_id,payload_hash,result FROM asset_skill_casts WHERE operation_id=$1 OR(actor_id=$2 AND session_id=$3 AND generation=$4 AND client_tick=$5)`, q.OperationID, q.Session.CharacterID, q.Session.SessionID, q.Session.Generation, int64(q.ClientTick)).Scan(&oldOp, &oldHash, &raw)
	if e == nil {
		if oldOp != q.OperationID || oldHash != hash {
			return r, domain.ErrForbidden
		}
		if e = json.Unmarshal(raw, &r); e != nil {
			return r, e
		}
		r.Replayed = true
		return r, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return r, e
	}
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1450036,$1)`, q.Session.Lease.WorldID); e != nil {
		return r, e
	}
	partyID, e := partyForCharacter(ctx, tx, q.Session.CharacterID)
	if e != nil {
		return r, e
	}
	if partyID != q.PartyID {
		return r, domain.ErrStaleOwner
	}
	party, e := readPartySnapshot(ctx, tx, partyID, q.Session.Lease.WorldID)
	if e != nil {
		return r, e
	}
	if party.Revision != q.PartyRevision || len(party.Members) != len(q.PartyMembers) {
		return r, domain.ErrStaleOwner
	}
	wanted := map[int64]bool{}
	for _, id := range q.PartyMembers {
		if wanted[id] {
			return r, domain.ErrInvalidInput
		}
		wanted[id] = true
	}
	for _, m := range party.Members {
		if !wanted[m.CharacterID] {
			return r, domain.ErrStaleOwner
		}
	}
	if partyID == 0 && (len(q.Members) != 1 || len(q.PartyMembers) != 0 || q.PartyRevision != 0) {
		return r, domain.ErrInvalidInput
	}
	members := append([]domain.CharacterSession(nil), q.Members...)
	sort.Slice(members, func(i, j int) bool { return members[i].CharacterID < members[j].CharacterID })
	chars := map[int64]domain.Character{}
	for _, m := range members {
		if m.CharacterID != q.Session.CharacterID && !wanted[m.CharacterID] {
			return r, domain.ErrForbidden
		}
		c, e := lockInventoryCharacter(ctx, tx, m)
		if e != nil {
			return r, e
		}
		if c.MapID != m.Character.MapID || c.Job != m.Character.Job || c.Level != m.Character.Level {
			return r, domain.ErrStaleOwner
		}
		if e = carryCombatProgress(&c, m); e != nil {
			return r, e
		}
		c.X, c.Y = m.Character.X, m.Character.Y
		chars[c.ID] = c
	}
	now := time.Time{}
	if e = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		return r, e
	}
	actor := chars[q.Session.CharacterID]
	if actor.HP <= 0 {
		return r, domain.ErrForbidden
	}
	for _, cool := range actor.SkillCooldowns {
		if cool.ID == q.SkillID && cool.ExpiresAt.After(now) {
			return r, domain.ErrAlreadyExists
		}
	}
	plan, e := skills.PartyCast(os.Getenv("MAPLEGO_WZ_ROOT"), actor, q.SkillID, q.ClientLevel, now)
	if e != nil {
		return r, domain.ErrForbidden
	}
	if plan.Toggle && skills.ActiveBuff(actor, q.SkillID, now) {
		// A toggle recast removes the skill from the caster and the party
		// at no cost.
		r.ToggledOff = true
		for _, m := range members {
			c := chars[m.CharacterID]
			result := domain.SkillResult{}
			if e = removeSkillBuffs(ctx, tx, &c, &result, func(b domain.SkillBuff) bool { return b.ID == q.SkillID }); e != nil {
				return r, e
			}
			if e = commitInventoryState(ctx, tx, &c); e != nil {
				return r, e
			}
			r.Recipients = append(r.Recipients, domain.PartySkillRecipient{Session: m, Character: c, Expired: result.Expired})
		}
		return s.finishPartySkillCast(ctx, tx, q, hash, r)
	}
	rows, ok := skillBuffRows(q.SkillID, q.ClientLevel, plan.Party, now)
	if !ok {
		return r, domain.ErrInvalidInput
	}
	var paid domain.SkillResult
	if e = payCastCost(ctx, tx, &actor, &paid, q.SkillID, plan, now); e != nil {
		return r, e
	}
	if e = retimeSkillBuffs(ctx, tx, &actor, &paid, plan.Retime, now); e != nil {
		return r, e
	}
	chars[actor.ID] = actor
	for _, m := range members {
		c := chars[m.CharacterID]
		if c.ID != actor.ID && !skills.PartyRecipient(plan, actor, c) {
			continue
		}
		if c.ID != actor.ID && plan.Revive {
			c.HP = c.MaxHP // Java Resurrection heals the dead member to full.
		}
		if c.VitalsModel != 1 && touchesMaxima(rows) {
			if c.ID == actor.ID {
				return r, domain.ErrForbidden
			}
			continue // MAXHP/MAXMP needs the explicit base-vitals model.
		}
		receiver := domain.PartySkillRecipient{Session: m}
		result := domain.SkillResult{}
		if c.ID == actor.ID {
			result.Inventory, result.Updated = paid.Inventory, paid.Updated
		}
		if e = applyCastEffects(ctx, tx, &c, &result, q.SkillID, plan, rows); e != nil {
			return r, e
		}
		receiver.Expired, receiver.Inventory, receiver.ResetCooldowns, receiver.Updated = result.Expired, result.Inventory, result.ResetCooldowns, result.Updated
		if e = commitInventoryState(ctx, tx, &c); e != nil {
			return r, e
		}
		receiver.Character = c
		r.Recipients = append(r.Recipients, receiver)
	}
	return s.finishPartySkillCast(ctx, tx, q, hash, r)
}

// finishPartySkillCast records the receipt and commits.
func (s *Store) finishPartySkillCast(ctx context.Context, tx *sql.Tx, q domain.PartySkillCastRequest, hash string, r domain.PartySkillCastResult) (domain.PartySkillCastResult, error) {
	raw, e := json.Marshal(r)
	if e != nil {
		return r, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO asset_skill_casts(operation_id,actor_id,world_id,session_id,generation,client_tick,skill_id,payload_hash,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, q.OperationID, q.Session.CharacterID, q.Session.Lease.WorldID, q.Session.SessionID, q.Session.Generation, int64(q.ClientTick), q.SkillID, hash, raw); e != nil {
		return r, e
	}
	if e = tx.Commit(); e != nil {
		return r, e
	}
	for _, v := range r.Recipients {
		s.roster.Invalidate(ctx, v.Character.AccountID, v.Character.WorldID)
	}
	return r, nil
}
