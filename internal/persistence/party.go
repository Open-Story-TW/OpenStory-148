package persistence

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"maple-go-server/internal/domain"
)

var _ domain.PartyRepository = (*Store)(nil)

// Only party commands serialize per world; gameplay/checkpoints never take
// this lock. It also orders cross-party joins without character lock cycles.
func guardParty(ctx context.Context, tx *sql.Tx, s domain.CharacterSession) error {
	if err := guardBuddyOwner(ctx, tx, s); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1450036,$1)`, s.Lease.WorldID)
	return err
}

func partyForCharacter(ctx context.Context, tx *sql.Tx, id int64) (int64, error) {
	var party int64
	err := tx.QueryRowContext(ctx, `SELECT party_id FROM game_party_members WHERE character_id=$1`, id).Scan(&party)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return party, err
}

func readPartySnapshot(ctx context.Context, tx *sql.Tx, id int64, world int) (domain.PartySnapshot, error) {
	p := domain.PartySnapshot{}
	if id == 0 {
		return p, nil
	}
	err := tx.QueryRowContext(ctx, `SELECT id,world_id,leader_id,revision,setting FROM game_parties WHERE id=$1 AND world_id=$2`, id, world).Scan(&p.ID, &p.WorldID, &p.LeaderID, &p.Revision, &p.Setting)
	if errors.Is(err, sql.ErrNoRows) {
		return p, domain.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT m.character_id,m.member_slot,c.name,c.level,c.job,c.subcategory FROM game_party_members m JOIN game_characters c ON c.id=m.character_id WHERE m.party_id=$1 AND c.world_id=$2 ORDER BY m.member_slot`, id, world)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var m domain.PartyMember
		if err = rows.Scan(&m.CharacterID, &m.Slot, &m.Name, &m.Level, &m.Job, &m.SubJob); err != nil {
			return p, err
		}
		p.Members = append(p.Members, m)
	}
	return p, rows.Err()
}

func readPartyInvitations(ctx context.Context, tx *sql.Tx, id int64, world int) ([]domain.PartyInvitation, error) {
	rows, err := tx.QueryContext(ctx, `SELECT i.party_id,i.inviter_id,i.target_id,c.name,c.level,c.job,c.subcategory,i.expires_at FROM game_party_invitations i JOIN game_parties p ON p.id=i.party_id JOIN game_characters c ON c.id=i.inviter_id WHERE i.target_id=$1 AND p.world_id=$2 AND i.expires_at>clock_timestamp() AND i.leader_epoch=p.leader_epoch AND i.inviter_id=p.leader_id ORDER BY i.expires_at,i.party_id`, id, world)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var invites []domain.PartyInvitation
	for rows.Next() {
		var i domain.PartyInvitation
		if err = rows.Scan(&i.PartyID, &i.InviterID, &i.TargetID, &i.InviterName, &i.InviterLevel, &i.InviterJob, &i.InviterSubJob, &i.ExpiresAt); err != nil {
			return nil, err
		}
		invites = append(invites, i)
	}
	return invites, rows.Err()
}

func (s *Store) ReadParty(ctx context.Context, session domain.CharacterSession) (domain.PartyState, error) {
	var state domain.PartyState
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return state, err
	}
	defer tx.Rollback()
	if err = guardParty(ctx, tx, session); err != nil {
		return state, err
	}
	id, err := partyForCharacter(ctx, tx, session.CharacterID)
	if err != nil {
		return state, err
	}
	if state.Party, err = readPartySnapshot(ctx, tx, id, session.Lease.WorldID); err != nil {
		return state, err
	}
	if id == 0 {
		state.Invitations, err = readPartyInvitations(ctx, tx, session.CharacterID, session.Lease.WorldID)
	}
	return state, err
}

func createPartySQL(ctx context.Context, tx *sql.Tx, session domain.CharacterSession) (int64, error) {
	var id int64
	// Recheck DB job/level rather than a caller's detached character snapshot.
	var level, job int
	if err := tx.QueryRowContext(ctx, `SELECT level,job FROM game_characters WHERE id=$1 AND account_id=$2 AND world_id=$3`, session.CharacterID, session.AccountID, session.Lease.WorldID).Scan(&level, &job); err != nil {
		return 0, err
	}
	if job == 0 && level < 10 {
		return 0, domain.ErrForbidden
	}
	err := tx.QueryRowContext(ctx, `INSERT INTO game_parties(world_id,leader_id) VALUES($1,$2) RETURNING id`, session.Lease.WorldID, session.CharacterID).Scan(&id)
	if err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO game_party_members(character_id,party_id,member_slot) VALUES($1,$2,0)`, session.CharacterID, id)
	}
	return id, err
}

func (s *Store) MutateParty(ctx context.Context, q domain.PartyRequest) (domain.PartyMutation, error) {
	switch q.Action {
	case domain.PartyJoin, domain.PartyRemove, domain.PartySetLeader, domain.PartyDisband:
		return s.mutateExpeditionParty(ctx, q)
	}
	result := domain.PartyMutation{Action: q.Action, TargetID: q.TargetID}
	switch q.Action {
	case domain.PartyCreate, domain.PartyLeave, domain.PartyInvite, domain.PartyExpel, domain.PartyLeader, domain.PartySetting, domain.PartyAcknowledge, domain.PartyAccept, domain.PartyPending, domain.PartyDuplicate, domain.PartyDeny:
	default:
		return result, domain.ErrInvalidInput
	}
	if q.Action == domain.PartyInvite && (!domain.ValidBuddyText(q.Name, 13) || q.TargetID <= 0) {
		return result, domain.ErrInvalidInput
	}
	if (q.Action == domain.PartyExpel || q.Action == domain.PartyLeader) && q.TargetID <= 0 {
		return result, domain.ErrInvalidInput
	}
	if q.Action >= domain.PartyAcknowledge && q.PartyID <= 0 {
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
	partyID, err := partyForCharacter(ctx, tx, actor)
	if err != nil {
		return result, err
	}
	result.Before, err = readPartySnapshot(ctx, tx, partyID, world)
	if err != nil {
		return result, err
	}
	if q.Action == domain.PartyCreate || (q.Action == domain.PartyInvite && partyID == 0) {
		if partyID == 0 {
			partyID, err = createPartySQL(ctx, tx, q.Session)
			if err != nil {
				return result, err
			}
			result.Created = true
		}
		result.After, err = readPartySnapshot(ctx, tx, partyID, world)
		if err != nil {
			return result, err
		}
	}
	switch q.Action {
	case domain.PartyCreate:
		// Duplicate create never inserts another party or increments revision.
		if result.After.LeaderID != actor {
			return result, domain.ErrAlreadyExists
		}
	case domain.PartyInvite:
		p := result.Before
		if result.Created {
			p = result.After
		}
		if p.ID == 0 || p.LeaderID != actor || q.TargetID == actor {
			return result, domain.ErrForbidden
		}
		if len(p.Members) >= domain.PartyCapacity {
			return result, domain.ErrPartyFull
		}
		var target int64
		// Invites target only a current world character, never a name cached from
		// another process/channel or an offline character. World also checks its peer.
		err = tx.QueryRowContext(ctx, `SELECT c.id FROM game_characters c JOIN ops_character_ownership o ON o.character_id=c.id JOIN ops_account_sessions a ON a.account_id=o.account_id AND a.session_id=o.session_id AND a.generation=o.generation WHERE c.id=$1 AND c.name_key=$2 AND c.world_id=$3 AND o.world_id=$3 AND o.instance_id=$4 AND o.epoch=$5 AND a.state='world' AND a.character_id=c.id AND a.owner_instance=o.instance_id AND a.world_epoch=o.epoch AND a.world_id=o.world_id FOR SHARE OF o,a`, q.TargetID, strings.ToLower(q.Name), world, q.Session.Lease.InstanceID, q.Session.Lease.Epoch).Scan(&target)
		if errors.Is(err, sql.ErrNoRows) {
			return result, domain.ErrNotFound
		}
		if err != nil {
			return result, err
		}
		other, err := partyForCharacter(ctx, tx, target)
		if err != nil {
			return result, err
		}
		if other != 0 {
			return result, domain.ErrAlreadyExists
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO game_party_invitations(party_id,target_id,inviter_id,leader_epoch,expires_at) SELECT id,$2,$3,leader_epoch,clock_timestamp()+interval '60 seconds' FROM game_parties WHERE id=$1 ON CONFLICT(party_id,target_id) DO UPDATE SET inviter_id=EXCLUDED.inviter_id,leader_epoch=EXCLUDED.leader_epoch,expires_at=CASE WHEN game_party_invitations.expires_at>clock_timestamp() AND game_party_invitations.leader_epoch=EXCLUDED.leader_epoch THEN game_party_invitations.expires_at ELSE EXCLUDED.expires_at END`, partyID, target, actor)
		if err != nil {
			return result, err
		}
		invites, err := readPartyInvitations(ctx, tx, target, world)
		if err != nil {
			return result, err
		}
		for _, i := range invites {
			if i.PartyID == partyID {
				copy := i
				result.Invitation = &copy
				break
			}
		}
		result.After = p
	case domain.PartyAcknowledge, domain.PartyPending, domain.PartyDuplicate, domain.PartyDeny, domain.PartyAccept:
		if q.Action == domain.PartyAccept && partyID == q.PartyID {
			result.After = result.Before
			break
		}
		invites, err := readPartyInvitations(ctx, tx, actor, world)
		if err != nil {
			return result, err
		}
		var invite *domain.PartyInvitation
		for _, i := range invites {
			if i.PartyID == q.PartyID {
				copy := i
				invite = &copy
				break
			}
		}
		if invite == nil {
			return result, domain.ErrPartyInvite
		}
		result.TargetID = invite.InviterID
		result.After = result.Before
		if q.Action == domain.PartyDeny {
			_, err = tx.ExecContext(ctx, `DELETE FROM game_party_invitations WHERE party_id=$1 AND target_id=$2`, q.PartyID, actor)
			break
		}
		if q.Action != domain.PartyAccept {
			break
		} // 26/28/29 cannot grant membership.
		if partyID != 0 {
			return result, domain.ErrAlreadyExists
		}
		p, err := readPartySnapshot(ctx, tx, q.PartyID, world)
		if err != nil {
			return result, err
		}
		if len(p.Members) >= domain.PartyCapacity {
			return result, domain.ErrPartyFull
		}
		result.Before = p
		partyID = p.ID
		used := [domain.PartyCapacity]bool{}
		for _, m := range p.Members {
			used[m.Slot] = true
		}
		slot := 0
		for used[slot] {
			slot++
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO game_party_members(character_id,party_id,member_slot) VALUES($1,$2,$3)`, actor, partyID, slot)
		if err == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM game_party_invitations WHERE target_id=$1`, actor)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE game_parties SET revision=revision+1 WHERE id=$1`, partyID)
		}
	case domain.PartyLeave, domain.PartyExpel, domain.PartyLeader, domain.PartySetting:
		p := result.Before
		if partyID == 0 {
			if q.Action == domain.PartyLeave {
				return result, nil
			}
			return result, domain.ErrForbidden
		}
		if q.Action != domain.PartyLeave && p.LeaderID != actor {
			return result, domain.ErrForbidden
		}
		if q.Action == domain.PartyExpel || q.Action == domain.PartyLeader {
			if q.TargetID == actor {
				return result, domain.ErrForbidden
			}
			if _, ok := p.Member(q.TargetID); !ok {
				return result, domain.ErrNotFound
			}
		}
		switch q.Action {
		case domain.PartyLeave, domain.PartyExpel:
			target := q.TargetID
			if q.Action == domain.PartyLeave {
				target = actor
			}
			result.TargetID = target
			if target == p.LeaderID {
				_, err = tx.ExecContext(ctx, `DELETE FROM game_parties WHERE id=$1`, partyID)
				partyID = 0
			} else {
				_, err = tx.ExecContext(ctx, `DELETE FROM game_party_members WHERE party_id=$1 AND character_id=$2`, partyID, target)
				if err == nil {
					_, err = tx.ExecContext(ctx, `UPDATE game_parties SET revision=revision+1 WHERE id=$1`, partyID)
				}
			}
		case domain.PartyLeader:
			_, err = tx.ExecContext(ctx, `UPDATE game_parties SET leader_id=$2,leader_epoch=leader_epoch+1,revision=revision+1 WHERE id=$1`, partyID, q.TargetID)
			if err == nil {
				_, err = tx.ExecContext(ctx, `DELETE FROM game_party_invitations WHERE party_id=$1`, partyID)
			}
		case domain.PartySetting:
			_, err = tx.ExecContext(ctx, `UPDATE game_parties SET setting=$2,revision=revision+CASE WHEN setting<>$2 THEN 1 ELSE 0 END WHERE id=$1`, partyID, q.Setting)
		}
	}
	if err != nil {
		return result, err
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
