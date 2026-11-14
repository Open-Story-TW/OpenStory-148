package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"maple-go-server/internal/domain"
)

var _ domain.AllianceRepository = (*Store)(nil)

func readAllianceSnapshot(ctx context.Context, q sqlQuerier, id int64) (domain.AllianceSnapshot, error) {
	a := domain.AllianceSnapshot{}
	if id == 0 {
		return a, nil
	}
	var titles []byte
	err := q.QueryRowContext(ctx, `SELECT id,world_id,name,leader_id,array_to_json(titles)::text,capacity,notice,revision FROM game_alliances WHERE id=$1`, id).Scan(&a.ID, &a.WorldID, &a.Name, &a.LeaderID, &titles, &a.Capacity, &a.Notice, &a.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return a, domain.ErrNotFound
	}
	if err != nil {
		return a, err
	}
	var list []string
	if err = json.Unmarshal(titles, &list); err != nil {
		return a, err
	}
	copy(a.Titles[:], list)
	rows, err := q.QueryContext(ctx, `SELECT id FROM game_guilds WHERE alliance_id=$1 ORDER BY alliance_joined_at,id`, id)
	if err != nil {
		return a, err
	}
	var ids []int64
	for rows.Next() {
		var gid int64
		if err = rows.Scan(&gid); err != nil {
			rows.Close()
			return a, err
		}
		ids = append(ids, gid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return a, err
	}
	for _, gid := range ids {
		g, err := readGuildSnapshot(ctx, q, gid)
		if err != nil {
			return a, err
		}
		a.Guilds = append(a.Guilds, g)
	}
	return a, nil
}

func (s *Store) ReadAlliance(ctx context.Context, session domain.CharacterSession) (domain.AllianceSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.AllianceSnapshot{}, err
	}
	defer tx.Rollback()
	if err = guardBuddyOwner(ctx, tx, session); err != nil {
		return domain.AllianceSnapshot{}, err
	}
	var aid int64
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(g.alliance_id,0) FROM game_guild_members m JOIN game_guilds g ON g.id=m.guild_id WHERE m.character_id=$1`, session.CharacterID).Scan(&aid)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AllianceSnapshot{}, nil
	}
	if err != nil {
		return domain.AllianceSnapshot{}, err
	}
	return readAllianceSnapshot(ctx, tx, aid)
}

func validAllianceRequest(q domain.AllianceRequest) bool {
	if !validGMOperation(q.OperationID) {
		return false
	}
	switch q.Action {
	case domain.AllianceCreate:
		n, ok := big5Len(q.Name)
		return ok && n >= 2 && n <= 12 && q.PartnerID > 0 && validGuildText(q.Name, 12, false)
	case domain.AllianceInvite:
		return validGuildText(q.Name, 13, false)
	case domain.AllianceAccept, domain.AllianceDeny, domain.AllianceLeave, domain.AllianceCapacity, domain.AllianceDisband:
		return true
	case domain.AllianceExpel:
		return q.GuildID > 0
	case domain.AllianceLeader:
		return q.TargetID > 0
	case domain.AllianceRank:
		return q.TargetID > 0 && (q.Rank >= 3 && q.Rank <= 5 || q.Rank == 0 && (q.RankStep == 1 || q.RankStep == -1))
	case domain.AllianceTitles:
		for i, t := range q.Titles {
			if !validGuildText(t, 12, i >= 2) {
				return false
			}
		}
		return true
	case domain.AllianceNotice:
		return validGuildText(q.Notice, 100, true)
	}
	return false
}

// setAllianceRanks applies Legacy changeARank: the guild master gets master
// (1 for the alliance leader's guild, else 2) and members 3; 5 when leaving.
func setAllianceRanks(ctx context.Context, tx *sql.Tx, gid int64, masterRank, memberRank int) error {
	_, err := tx.ExecContext(ctx, `UPDATE game_guild_members SET alliance_rank=CASE WHEN rank=1 THEN $2::smallint ELSE $3::smallint END WHERE guild_id=$1`, gid, masterRank, memberRank)
	return err
}

func (s *Store) MutateAlliance(ctx context.Context, q domain.AllianceRequest) (domain.AllianceMutation, error) {
	result := domain.AllianceMutation{Action: q.Action}
	if !validAllianceRequest(q) {
		return result, domain.ErrInvalidInput
	}
	session := q.Session
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	// Alliances share the guild world lock: they rewrite guild rows.
	if err = guardGuild(ctx, tx, session); err != nil {
		return result, err
	}
	var prior int
	if err = tx.QueryRowContext(ctx, `SELECT 1 FROM game_guild_operations WHERE character_id=$1 AND operation_id=$2`, session.CharacterID, q.OperationID).Scan(&prior); err == nil {
		return result, domain.ErrAlreadyExists
	} else if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	gid, rank, err := guildFor(ctx, tx, session.CharacterID)
	if err != nil {
		return result, err
	}
	if gid == 0 {
		return result, domain.ErrNotFound
	}
	own, err := readGuildSnapshot(ctx, tx, gid)
	if err != nil {
		return result, err
	}
	aid := own.Alliance
	if result.Before, err = readAllianceSnapshot(ctx, tx, aid); err != nil {
		return result, err
	}
	me, _ := own.Member(session.CharacterID)
	world := session.Lease.WorldID
	isLeader := aid != 0 && result.Before.LeaderID == session.CharacterID
	exec := func(query string, args ...any) {
		if err == nil {
			_, err = tx.ExecContext(ctx, query, args...)
		}
	}
	after := aid
	switch q.Action {
	case domain.AllianceCreate:
		if rank != domain.GuildMaster || aid != 0 {
			return result, domain.ErrForbidden
		}
		pgid, prank, e := guildFor(ctx, tx, q.PartnerID)
		if e != nil {
			return result, e
		}
		if pgid == 0 || pgid == gid || prank != domain.GuildMaster {
			return result, domain.ErrForbidden
		}
		partner, e := readGuildSnapshot(ctx, tx, pgid)
		if e != nil {
			return result, e
		}
		if partner.Alliance != 0 || partner.WorldID != world {
			return result, domain.ErrForbidden
		}
		var taken bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM game_alliances WHERE world_id=$1 AND lower(name)=lower($2))`, world, q.Name).Scan(&taken); err != nil {
			return result, err
		}
		if taken {
			return result, domain.ErrAlreadyExists
		}
		if err = tx.QueryRowContext(ctx, `INSERT INTO game_alliances(world_id,name,leader_id,titles) VALUES($1,$2,$3,ARRAY['聯盟長','副聯盟長','成員','成員','成員']) RETURNING id`, world, q.Name, session.CharacterID).Scan(&after); err != nil {
			return result, err
		}
		exec(`UPDATE game_guilds SET alliance_id=$2,alliance_joined_at=clock_timestamp(),revision=revision+1 WHERE id=$1`, gid, after)
		exec(`UPDATE game_guilds SET alliance_id=$2,alliance_joined_at=clock_timestamp()+interval '1 microsecond',revision=revision+1 WHERE id=$1`, pgid, after)
		if err == nil {
			err = setAllianceRanks(ctx, tx, gid, 1, 3)
		}
		if err == nil {
			err = setAllianceRanks(ctx, tx, pgid, 2, 3)
		}
		result.GuildID = pgid
	case domain.AllianceInvite:
		if !isLeader || me.AllianceRank != 1 {
			return result, domain.ErrForbidden
		}
		var target, tgid int64
		var trank int
		e := tx.QueryRowContext(ctx, `SELECT c.id,m.guild_id,m.rank FROM game_characters c JOIN game_guild_members m ON m.character_id=c.id WHERE lower(c.name)=lower($1) AND c.world_id=$2`, q.Name, world).Scan(&target, &tgid, &trank)
		if errors.Is(e, sql.ErrNoRows) || e == nil && trank != domain.GuildMaster {
			return result, domain.ErrNotFound
		}
		if e != nil {
			return result, e
		}
		invited, e := readGuildSnapshot(ctx, tx, tgid)
		if e != nil {
			return result, e
		}
		if invited.Alliance != 0 {
			return result, domain.ErrAlreadyExists
		}
		if len(result.Before.Guilds) >= result.Before.Capacity {
			return result, domain.ErrGuildFull
		}
		exec(`INSERT INTO game_alliance_invitations(guild_id,alliance_id,inviter_id,expires_at) VALUES($1,$2,$3,clock_timestamp()+interval '20 minutes')
		ON CONFLICT(guild_id) DO UPDATE SET alliance_id=EXCLUDED.alliance_id,inviter_id=EXCLUDED.inviter_id,expires_at=EXCLUDED.expires_at`, tgid, aid, session.CharacterID)
		result.GuildID, result.InviteeID, result.Guild = tgid, target, invited
	case domain.AllianceAccept, domain.AllianceDeny:
		if rank != domain.GuildMaster || aid != 0 {
			return result, domain.ErrForbidden
		}
		var invitedTo, inviter int64
		e := tx.QueryRowContext(ctx, `DELETE FROM game_alliance_invitations i USING game_characters c WHERE i.guild_id=$1 AND c.id=i.inviter_id AND i.expires_at>clock_timestamp() RETURNING i.alliance_id,i.inviter_id,c.name`, gid).Scan(&invitedTo, &inviter, &result.InviterName)
		if errors.Is(e, sql.ErrNoRows) {
			return result, domain.ErrNotFound
		}
		if e != nil {
			return result, e
		}
		result.InviteeID = inviter
		if q.Action == domain.AllianceDeny {
			after = 0
			break
		}
		target, e := readAllianceSnapshot(ctx, tx, invitedTo)
		if e != nil {
			return result, e
		}
		if len(target.Guilds) >= target.Capacity {
			return result, domain.ErrGuildFull
		}
		after, result.Before, result.GuildID = invitedTo, target, gid
		exec(`UPDATE game_guilds SET alliance_id=$2,alliance_joined_at=clock_timestamp(),revision=revision+1 WHERE id=$1`, gid, invitedTo)
		if err == nil {
			err = setAllianceRanks(ctx, tx, gid, 2, 3)
		}
	case domain.AllianceLeave, domain.AllianceExpel:
		target := gid
		if q.Action == domain.AllianceExpel {
			target = q.GuildID
			if !isLeader || target == gid {
				return result, domain.ErrForbidden
			}
			result.Expelled = true
		} else if aid == 0 || rank != domain.GuildMaster || isLeader {
			return result, domain.ErrForbidden
		}
		var member int64
		if e := tx.QueryRowContext(ctx, `SELECT COALESCE(alliance_id,0) FROM game_guilds WHERE id=$1`, target).Scan(&member); e != nil || member != aid {
			return result, errors.Join(e, domain.ErrNotFound)
		}
		exec(`UPDATE game_guilds SET alliance_id=NULL,alliance_joined_at=NULL,revision=revision+1 WHERE id=$1`, target)
		if err == nil {
			err = setAllianceRanks(ctx, tx, target, 5, 5)
		}
		result.GuildID = target
	case domain.AllianceLeader:
		if !isLeader {
			return result, domain.ErrForbidden
		}
		var tgid int64
		var trank int
		if e := tx.QueryRowContext(ctx, `SELECT m.guild_id,m.rank FROM game_guild_members m JOIN game_guilds g ON g.id=m.guild_id WHERE m.character_id=$1 AND g.alliance_id=$2`, q.TargetID, aid).Scan(&tgid, &trank); e != nil || trank != domain.GuildMaster || tgid == gid {
			return result, errors.Join(e, domain.ErrForbidden)
		}
		exec(`UPDATE game_guild_members SET alliance_rank=2 WHERE character_id=$1`, session.CharacterID)
		exec(`UPDATE game_guild_members SET alliance_rank=1 WHERE character_id=$1`, q.TargetID)
		exec(`UPDATE game_alliances SET leader_id=$2,revision=revision+1 WHERE id=$1`, aid, q.TargetID)
	case domain.AllianceTitles:
		if !isLeader {
			return result, domain.ErrForbidden
		}
		raw, _ := json.Marshal(q.Titles[:])
		exec(`UPDATE game_alliances SET titles=ARRAY(SELECT jsonb_array_elements_text($2::jsonb)),revision=revision+1 WHERE id=$1`, aid, string(raw))
	case domain.AllianceRank:
		if aid == 0 || me.AllianceRank > 2 {
			return result, domain.ErrForbidden
		}
		var current int
		if e := tx.QueryRowContext(ctx, `SELECT m.alliance_rank FROM game_guild_members m JOIN game_guilds g ON g.id=m.guild_id WHERE m.character_id=$1 AND g.alliance_id=$2`, q.TargetID, aid).Scan(&current); e != nil || current <= 2 {
			return result, errors.Join(e, domain.ErrForbidden)
		}
		next := q.Rank
		if next == 0 {
			// Raising lowers the number; members stay within 3-5 (Legacy
			// changeAllianceRank).
			next = current - q.RankStep
			if next < 3 || next > 5 {
				return result, domain.ErrForbidden
			}
		}
		result.Rank = next
		exec(`UPDATE game_guild_members SET alliance_rank=$2 WHERE character_id=$1`, q.TargetID, next)
		exec(`UPDATE game_alliances SET revision=revision+1 WHERE id=$1`, aid)
	case domain.AllianceNotice:
		if aid == 0 || me.AllianceRank > 2 {
			return result, domain.ErrForbidden
		}
		exec(`UPDATE game_alliances SET notice=$2,revision=revision+1 WHERE id=$1`, aid, q.Notice)
	case domain.AllianceCapacity:
		if !isLeader {
			return result, domain.ErrForbidden
		}
		if result.Before.Capacity >= domain.AllianceMaxGuilds {
			return result, domain.ErrGuildFull
		}
		c, e := debitGuildMesos(ctx, tx, session, domain.AllianceCapacityFee, q.OperationID, "alliance_capacity")
		if e != nil {
			return result, e
		}
		result.Character = &c
		exec(`UPDATE game_alliances SET capacity=capacity+1,revision=revision+1 WHERE id=$1`, aid)
	case domain.AllianceDisband:
		if !isLeader {
			return result, domain.ErrForbidden
		}
		for _, g := range result.Before.Guilds {
			if err == nil {
				err = setAllianceRanks(ctx, tx, g.ID, 5, 5)
			}
		}
		exec(`DELETE FROM game_alliances WHERE id=$1`, aid)
		result.Disbanded, after = true, 0
	}
	if err != nil {
		return result, err
	}
	if after != 0 {
		if result.After, err = readAllianceSnapshot(ctx, tx, after); err != nil {
			return result, err
		}
	}
	if result.GuildID != 0 {
		if result.Guild, err = readGuildSnapshot(ctx, tx, result.GuildID); err != nil {
			return result, err
		}
	}
	receipt := result
	receipt.Character = nil
	raw, err := json.Marshal(receipt)
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO game_guild_operations(character_id,operation_id,action,result) VALUES($1,$2,$3,$4)`, session.CharacterID, q.OperationID, "alliance_"+string(q.Action), raw); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	if result.Character != nil {
		s.roster.Invalidate(ctx, result.Character.AccountID, result.Character.WorldID)
	}
	return result, nil
}
