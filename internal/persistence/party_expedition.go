package persistence

import (
	"context"

	"maple-go-server/internal/domain"
)

// mutateExpeditionParty applies the party changes an expedition makes. The
// world has already checked the actor against the expedition (its leader, or
// the member itself); here the actor's ownership is re-guarded and every
// membership change is applied atomically, as for the wire actions.
func (s *Store) mutateExpeditionParty(ctx context.Context, q domain.PartyRequest) (domain.PartyMutation, error) {
	result := domain.PartyMutation{Action: q.Action, TargetID: q.TargetID}
	if q.PartyID <= 0 || (q.Action != domain.PartyJoin && q.Action != domain.PartyDisband && q.TargetID <= 0) {
		return result, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = guardParty(ctx, tx, q.Session); err != nil {
		return result, err
	}
	actor, world := q.Session.CharacterID, q.Session.Lease.WorldID
	result.Before, err = readPartySnapshot(ctx, tx, q.PartyID, world)
	if err != nil {
		return result, err
	}
	p := result.Before
	partyID := p.ID
	switch q.Action {
	case domain.PartyJoin:
		current, err := partyForCharacter(ctx, tx, actor)
		if err != nil {
			return result, err
		}
		if current != 0 {
			return result, domain.ErrAlreadyExists
		}
		if len(p.Members) >= domain.PartyCapacity {
			return result, domain.ErrPartyFull
		}
		used := [domain.PartyCapacity]bool{}
		for _, m := range p.Members {
			used[m.Slot] = true
		}
		slot := 0
		for used[slot] {
			slot++
		}
		result.TargetID = actor
		_, err = tx.ExecContext(ctx, `INSERT INTO game_party_members(character_id,party_id,member_slot) VALUES($1,$2,$3)`, actor, partyID, slot)
		if err == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM game_party_invitations WHERE target_id=$1`, actor)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE game_parties SET revision=revision+1 WHERE id=$1`, partyID)
		}
		if err != nil {
			return result, err
		}
	case domain.PartyRemove:
		if _, ok := p.Member(q.TargetID); !ok {
			return result, domain.ErrNotFound
		}
		if len(p.Members) == 1 {
			_, err = tx.ExecContext(ctx, `DELETE FROM game_parties WHERE id=$1`, partyID)
			partyID = 0
		} else {
			if q.TargetID == p.LeaderID {
				next := int64(0)
				slot := domain.PartyCapacity
				for _, m := range p.Members {
					if m.CharacterID != q.TargetID && m.Slot < slot {
						next, slot = m.CharacterID, m.Slot
					}
				}
				_, err = tx.ExecContext(ctx, `UPDATE game_parties SET leader_id=$2,leader_epoch=leader_epoch+1 WHERE id=$1`, partyID, next)
				if err == nil {
					_, err = tx.ExecContext(ctx, `DELETE FROM game_party_invitations WHERE party_id=$1`, partyID)
				}
			}
			if err == nil {
				_, err = tx.ExecContext(ctx, `DELETE FROM game_party_members WHERE party_id=$1 AND character_id=$2`, partyID, q.TargetID)
			}
			if err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE game_parties SET revision=revision+1 WHERE id=$1`, partyID)
			}
		}
		if err != nil {
			return result, err
		}
	case domain.PartySetLeader:
		if _, ok := p.Member(q.TargetID); !ok {
			return result, domain.ErrNotFound
		}
		if q.TargetID != p.LeaderID {
			_, err = tx.ExecContext(ctx, `UPDATE game_parties SET leader_id=$2,leader_epoch=leader_epoch+1,revision=revision+1 WHERE id=$1`, partyID, q.TargetID)
			if err == nil {
				_, err = tx.ExecContext(ctx, `DELETE FROM game_party_invitations WHERE party_id=$1`, partyID)
			}
			if err != nil {
				return result, err
			}
		}
	case domain.PartyDisband:
		if _, err = tx.ExecContext(ctx, `DELETE FROM game_parties WHERE id=$1`, partyID); err != nil {
			return result, err
		}
		result.TargetID = p.LeaderID
		partyID = 0
	default:
		return result, domain.ErrInvalidInput
	}
	result.After, err = readPartySnapshot(ctx, tx, partyID, world)
	if err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return domain.PartyMutation{}, err
	}
	return result, nil
}
