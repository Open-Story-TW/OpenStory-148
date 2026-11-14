package persistence

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"maple-go-server/internal/domain"
)

var _ domain.BuddyRepository = (*Store)(nil)

// Buddy operations do not load or rewrite inventory, quests, skills or the
// character checkpoint. They still require the current world session/lease.
func guardBuddyOwner(ctx context.Context, tx *sql.Tx, session domain.CharacterSession) error {
	if session.CharacterID <= 0 || session.CharacterID != session.Character.ID || session.AccountID <= 0 {
		return domain.ErrForbidden
	}
	if err := guardLease(ctx, tx, session.Lease); err != nil {
		return err
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT o.character_id FROM ops_character_ownership o
	JOIN ops_account_sessions s ON s.account_id=o.account_id AND s.session_id=o.session_id AND s.generation=o.generation
	WHERE o.character_id=$1 AND o.account_id=$2 AND o.session_id=$3 AND o.generation=$4
	AND o.world_id=$5 AND o.instance_id=$6 AND o.epoch=$7 AND s.state='world'
	AND s.world_id=o.world_id AND s.character_id=o.character_id
	AND s.owner_instance=o.instance_id AND s.world_epoch=o.epoch FOR SHARE OF o,s`,
		session.CharacterID, session.AccountID, session.SessionID, session.Generation, session.Lease.WorldID, session.Lease.InstanceID, session.Lease.Epoch).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrStaleOwner
	}
	return err
}

func readBuddyList(ctx context.Context, tx *sql.Tx, id int64, world int) (domain.BuddyList, error) {
	list := domain.BuddyList{Capacity: domain.DefaultBuddyCapacity, Entries: []domain.BuddyEntry{}}
	err := tx.QueryRowContext(ctx, `SELECT capacity FROM game_buddy_profiles WHERE character_id=$1`, id).Scan(&list.Capacity)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return list, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT b.buddy_id,c.name,b.group_name,b.pending,c.level,c.job,c.subcategory FROM game_buddies b
	JOIN game_characters c ON c.id=b.buddy_id WHERE b.character_id=$1 AND c.world_id=$2 ORDER BY b.created_at,b.buddy_id`, id, world)
	if err != nil {
		return list, err
	}
	defer rows.Close()
	for rows.Next() {
		var e domain.BuddyEntry
		if err = rows.Scan(&e.CharacterID, &e.Name, &e.Group, &e.Pending, &e.Level, &e.Job, &e.SubJob); err != nil {
			return list, err
		}
		list.Entries = append(list.Entries, e)
	}
	return list, rows.Err()
}

func (s *Store) ReadBuddies(ctx context.Context, session domain.CharacterSession) (domain.BuddyList, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.BuddyList{}, err
	}
	defer tx.Rollback()
	if err = guardBuddyOwner(ctx, tx, session); err != nil {
		return domain.BuddyList{}, err
	}
	return readBuddyList(ctx, tx, session.CharacterID, session.Lease.WorldID)
}

func findBuddy(list domain.BuddyList, id int64) (domain.BuddyEntry, bool) {
	for _, e := range list.Entries {
		if e.CharacterID == id {
			return e, true
		}
	}
	return domain.BuddyEntry{}, false
}

func (s *Store) MutateBuddy(ctx context.Context, q domain.BuddyRequest) (domain.BuddyMutation, error) {
	var result domain.BuddyMutation
	if q.Action < 1 || q.Action > 3 || (q.Action == 1 && (!domain.ValidBuddyText(q.Name, 13) || !domain.ValidBuddyText(q.Group, domain.BuddyGroupLimit))) || (q.Action != 1 && q.TargetID <= 0) {
		return result, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = guardBuddyOwner(ctx, tx, q.Session); err != nil {
		return result, err
	}
	id, world := q.Session.CharacterID, q.Session.Lease.WorldID
	if q.Action == 1 {
		err = tx.QueryRowContext(ctx, `SELECT id FROM game_characters WHERE name_key=$1 AND world_id=$2`, strings.ToLower(q.Name), world).Scan(&q.TargetID)
		if errors.Is(err, sql.ErrNoRows) {
			return result, domain.ErrNotFound
		}
		if err != nil {
			return result, err
		}
	}
	if id == q.TargetID {
		return result, domain.ErrForbidden
	}
	// Lock both aggregate rows in ID order before checking capacities or writing
	// either direction. Concurrent reciprocal adds cannot deadlock or overfill.
	rows, err := tx.QueryContext(ctx, `SELECT id,account_id,world_id,name,level,job,subcategory FROM game_characters WHERE id IN ($1,$2) ORDER BY id FOR UPDATE`, id, q.TargetID)
	if err != nil {
		return result, err
	}
	found, actorValid, targetValid := 0, false, false
	for rows.Next() {
		var charID, account int64
		var charWorld, level, job, subJob int
		var name string
		if err = rows.Scan(&charID, &account, &charWorld, &name, &level, &job, &subJob); err != nil {
			rows.Close()
			return result, err
		}
		found++
		if charID == id {
			actorValid = account == q.Session.AccountID && charWorld == world
			result.SenderName, result.SenderLevel, result.SenderJob, result.SenderSubJob = name, level, job, subJob
		} else {
			targetValid = charWorld == world
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	if found != 2 || !actorValid || !targetValid {
		return result, domain.ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO game_buddy_profiles(character_id) VALUES ($1),($2) ON CONFLICT DO NOTHING`, id, q.TargetID); err != nil {
		return result, err
	}
	own, err := readBuddyList(ctx, tx, id, world)
	if err != nil {
		return result, err
	}
	other, err := readBuddyList(ctx, tx, q.TargetID, world)
	if err != nil {
		return result, err
	}
	entry, exists := findBuddy(own, q.TargetID)
	reverse, reverseExists := findBuddy(other, id)
	switch q.Action {
	case 1:
		if exists {
			if entry.Pending || entry.Group == q.Group {
				return result, domain.ErrAlreadyExists
			}
			_, err = tx.ExecContext(ctx, `UPDATE game_buddies SET group_name=$3,updated_at=clock_timestamp() WHERE character_id=$1 AND buddy_id=$2`, id, q.TargetID, q.Group)
		} else {
			if len(own.Entries) >= domain.EffectiveBuddyCapacity(own.Capacity) {
				return result, domain.ErrBuddyFull
			}
			if !reverseExists && len(other.Entries) >= domain.EffectiveBuddyCapacity(other.Capacity) {
				return result, domain.ErrBuddyTargetFull
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO game_buddies(character_id,buddy_id,group_name,pending) VALUES ($1,$2,$3,false)`, id, q.TargetID, q.Group)
			if err == nil && !reverseExists {
				_, err = tx.ExecContext(ctx, `INSERT INTO game_buddies(character_id,buddy_id,group_name,pending) VALUES ($1,$2,$3,true)`, q.TargetID, id, "群組未指定")
				result.Invitation = true
			}
		}
	case 2:
		if !exists || !reverseExists || reverse.Pending {
			return result, domain.ErrForbidden
		}
		if entry.Pending {
			_, err = tx.ExecContext(ctx, `UPDATE game_buddies SET pending=false,updated_at=clock_timestamp() WHERE character_id=$1 AND buddy_id=$2`, id, q.TargetID)
		}
	case 3:
		// Removing/rejecting is directed. Preserve the other player's accepted
		// entry but hide presence unless both directions are accepted. A sender
		// withdrawing an invitation also removes the receiver's pending entry.
		_, err = tx.ExecContext(ctx, `DELETE FROM game_buddies WHERE character_id=$1 AND buddy_id=$2 OR character_id=$2 AND buddy_id=$1 AND pending`, id, q.TargetID)
	}
	if err != nil {
		return result, err
	}
	result.TargetID = q.TargetID
	result.List, err = readBuddyList(ctx, tx, id, world)
	if err != nil {
		return result, err
	}
	result.TargetList, err = readBuddyList(ctx, tx, q.TargetID, world)
	if err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return domain.BuddyMutation{}, err
	}
	return result, nil
}
