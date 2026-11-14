package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"golang.org/x/text/encoding/traditionalchinese"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

var _ domain.GuildRepository = (*Store)(nil)

// Guild commands serialize per world (like parties), which also orders
// cross-guild invitation races without per-character lock cycles.
func guardGuild(ctx context.Context, tx *sql.Tx, s domain.CharacterSession) error {
	if err := guardBuddyOwner(ctx, tx, s); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1450042,$1)`, s.Lease.WorldID)
	return err
}

func guildFor(ctx context.Context, q sqlQuerier, characterID int64) (int64, int, error) {
	var id int64
	var rank int
	err := q.QueryRowContext(ctx, `SELECT guild_id,rank FROM game_guild_members WHERE character_id=$1`, characterID).Scan(&id, &rank)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	return id, rank, err
}

func readGuildSnapshot(ctx context.Context, q sqlQuerier, id int64) (domain.GuildSnapshot, error) {
	g := domain.GuildSnapshot{}
	if id == 0 {
		return g, nil
	}
	var titles []byte
	err := q.QueryRowContext(ctx, `SELECT id,world_id,name,leader_id,array_to_json(titles)::text,capacity,logo_bg,logo_bg_color,logo,logo_color,notice,gp,level,revision,COALESCE(alliance_id,0) FROM game_guilds WHERE id=$1`, id).Scan(&g.ID, &g.WorldID, &g.Name, &g.LeaderID, &titles, &g.Capacity, &g.Emblem.BG, &g.Emblem.BGColor, &g.Emblem.Logo, &g.Emblem.Color, &g.Notice, &g.GP, &g.Level, &g.Revision, &g.Alliance)
	if errors.Is(err, sql.ErrNoRows) {
		return g, domain.ErrNotFound
	}
	if err != nil {
		return g, err
	}
	var list []string
	if err = json.Unmarshal(titles, &list); err != nil {
		return g, err
	}
	copy(g.Titles[:], list)
	g.Capacity = domain.EffectiveGuildCapacity(g.Capacity)
	rows, err := q.QueryContext(ctx, `SELECT m.character_id,c.name,c.level,c.job,m.rank,m.contribution,m.alliance_rank FROM game_guild_members m JOIN game_characters c ON c.id=m.character_id WHERE m.guild_id=$1 ORDER BY m.joined_at,m.character_id`, id)
	if err != nil {
		return g, err
	}
	defer rows.Close()
	for rows.Next() {
		var m domain.GuildMemberInfo
		if err = rows.Scan(&m.CharacterID, &m.Name, &m.Level, &m.Job, &m.Rank, &m.Contribution, &m.AllianceRank); err != nil {
			return g, err
		}
		g.Members = append(g.Members, m)
	}
	if err = rows.Err(); err != nil {
		return g, err
	}
	rows.Close()
	skills, err := q.QueryContext(ctx, `SELECT skill_id,level,expires_at,purchaser,activator FROM game_guild_skills WHERE guild_id=$1 ORDER BY skill_id`, id)
	if err != nil {
		return g, err
	}
	defer skills.Close()
	for skills.Next() {
		var k domain.GuildSkillState
		if err = skills.Scan(&k.SkillID, &k.Level, &k.ExpiresAt, &k.Purchaser, &k.Activator); err != nil {
			return g, err
		}
		g.Skills = append(g.Skills, k)
	}
	return g, skills.Err()
}

func guildSkillLevel(g domain.GuildSnapshot, id int) domain.GuildSkillState {
	for _, k := range g.Skills {
		if k.SkillID == id {
			return k
		}
	}
	return domain.GuildSkillState{SkillID: id}
}

// gainGuildGP applies a GP delta (floor 0), the matching contribution and
// recalculates the level, like MapleGuild.gainGP.
func gainGuildGP(ctx context.Context, tx *sql.Tx, gid, characterID int64, amount int) error {
	var gp int
	if err := tx.QueryRowContext(ctx, `UPDATE game_guilds SET gp=GREATEST(0,gp+$2),revision=revision+1 WHERE id=$1 RETURNING gp`, gid, amount).Scan(&gp); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE game_guilds SET level=$2 WHERE id=$1`, gid, domain.GuildLevelFor(gp)); err != nil {
		return err
	}
	if characterID > 0 && amount > 0 {
		_, err := tx.ExecContext(ctx, `UPDATE game_guild_members SET contribution=contribution+$2 WHERE character_id=$1 AND guild_id=$3`, characterID, amount, gid)
		return err
	}
	return nil
}

// readGuildTag attaches the character's guild identity for field publication.
func readGuildTag(ctx context.Context, q txQuery, c *domain.Character) error {
	rows, err := q.QueryContext(ctx, `SELECT g.id,g.name,m.rank,g.logo_bg,g.logo_bg_color,g.logo,g.logo_color FROM game_guild_members m JOIN game_guilds g ON g.id=m.guild_id WHERE m.character_id=$1`, c.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	c.Guild = domain.GuildTag{}
	for rows.Next() {
		if err = rows.Scan(&c.Guild.ID, &c.Guild.Name, &c.Guild.Rank, &c.Guild.Emblem.BG, &c.Guild.Emblem.BGColor, &c.Guild.Emblem.Logo, &c.Guild.Emblem.Color); err != nil {
			return err
		}
	}
	return rows.Err()
}

func big5Len(v string) (int, bool) {
	p, err := traditionalchinese.Big5.NewEncoder().Bytes([]byte(v))
	return len(p), err == nil
}

// validGuildName mirrors Legacy isGuildNameAcceptable within the native field.
func validGuildName(name string) bool {
	n, ok := big5Len(name)
	return ok && n >= 3 && n <= 12 && !strings.ContainsAny(name, " \t\r\n\x00") && len([]rune(name)) >= 2
}

func validGuildText(v string, maxBytes int, allowEmpty bool) bool {
	n, ok := big5Len(v)
	return ok && n <= maxBytes && (allowEmpty || strings.TrimSpace(v) != "") && !strings.ContainsAny(v, "\r\n\x00")
}

func (s *Store) ReadGuild(ctx context.Context, session domain.CharacterSession) (domain.GuildSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.GuildSnapshot{}, err
	}
	defer tx.Rollback()
	if err = guardBuddyOwner(ctx, tx, session); err != nil {
		return domain.GuildSnapshot{}, err
	}
	id, _, err := guildFor(ctx, tx, session.CharacterID)
	if err != nil {
		return domain.GuildSnapshot{}, err
	}
	return readGuildSnapshot(ctx, tx, id)
}

// debitGuildMesos charges a guild fee through the meso ledger. The live
// revision must match: the caller holds persistMu, like item use.
func debitGuildMesos(ctx context.Context, tx *sql.Tx, session domain.CharacterSession, amount int64, operationID, reason string) (domain.Character, error) {
	var mesos, revision int64
	if err := tx.QueryRowContext(ctx, `SELECT mesos,revision FROM game_characters WHERE id=$1 AND account_id=$2 AND world_id=$3 FOR UPDATE`, session.CharacterID, session.AccountID, session.Lease.WorldID).Scan(&mesos, &revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = domain.ErrForbidden
		}
		return domain.Character{}, err
	}
	if revision != session.Character.Revision {
		return domain.Character{}, domain.ErrStaleOwner
	}
	if mesos < amount {
		return domain.Character{}, domain.ErrInsufficient
	}
	if _, err := tx.ExecContext(ctx, `UPDATE game_characters SET mesos=mesos-$1,revision=revision+1 WHERE id=$2`, amount, session.CharacterID); err != nil {
		return domain.Character{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO asset_meso_ledger(operation_id,character_id,account_id,world_id,delta,balance_after,reason) VALUES($1,$2,$3,$4,$5,$6,$7)`, operationID, session.CharacterID, session.AccountID, session.Lease.WorldID, -amount, mesos-amount, reason); err != nil {
		return domain.Character{}, err
	}
	value, err := readCharacterSQL(ctx, tx, session.CharacterID)
	if err != nil {
		return domain.Character{}, err
	}
	raw, err := marshalRoster(value)
	if err != nil {
		return domain.Character{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE game_character_roster SET snapshot=$1,revision=$2 WHERE character_id=$3`, raw, value.Revision, value.ID); err != nil {
		return domain.Character{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ops_outbox(event_type,account_id,world_id) VALUES('roster.invalidate',$1,$2)`, value.AccountID, value.WorldID)
	return value, err
}

func validGuildRequest(q domain.GuildRequest) bool {
	if !validGMOperation(q.OperationID) {
		return false
	}
	switch q.Action {
	case domain.GuildCreate:
		return validGuildName(q.Name)
	case domain.GuildInvite:
		return validGuildText(q.Name, 13, false)
	case domain.GuildAccept:
		return q.GuildID > 0
	case domain.GuildDeny:
		return true
	case domain.GuildLeave:
		return true
	case domain.GuildExpel, domain.GuildLeader:
		return q.TargetID > 0
	case domain.GuildChangeRank:
		return q.TargetID > 0 && q.Rank >= 2 && q.Rank <= 5
	case domain.GuildTitles:
		for i, t := range q.Titles {
			if !validGuildText(t, 12, i >= 2) {
				return false
			}
		}
		return true
	case domain.GuildEmblem:
		e := q.Emblem
		return e.BG >= 0 && e.BG <= 65535 && e.Logo >= 0 && e.Logo <= 65535 && e.BGColor >= 0 && e.BGColor <= 255 && e.Color >= 0 && e.Color <= 255
	case domain.GuildNotice:
		return validGuildText(q.Notice, 100, true)
	case domain.GuildDisband, domain.GuildCapacity:
		return true
	case domain.GuildSkillBuy, domain.GuildSkillRenew:
		return q.SkillID >= 91000000 && q.SkillID <= 91009999
	case domain.GuildGainGP:
		return q.Amount > 0 && q.Amount <= 100000 && q.Period != "" && len(q.Period) <= 64
	}
	return false
}

func (s *Store) MutateGuild(ctx context.Context, q domain.GuildRequest) (domain.GuildMutation, error) {
	result := domain.GuildMutation{Action: q.Action, TargetID: q.TargetID}
	if !validGuildRequest(q) {
		return result, domain.ErrInvalidInput
	}
	session := q.Session
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
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
	if result.Before, err = readGuildSnapshot(ctx, tx, gid); err != nil {
		return result, err
	}
	world := session.Lease.WorldID
	exec := func(query string, args ...any) error {
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, query, args...)
		return err
	}
	bump := func(id int64) error {
		return exec(`UPDATE game_guilds SET revision=revision+1 WHERE id=$1`, id)
	}
	targetRank := func() (int, error) {
		var r int
		e := tx.QueryRowContext(ctx, `SELECT m.rank,c.name FROM game_guild_members m JOIN game_characters c ON c.id=m.character_id WHERE m.character_id=$1 AND m.guild_id=$2`, q.TargetID, gid).Scan(&r, &result.TargetName)
		if errors.Is(e, sql.ErrNoRows) {
			return 0, domain.ErrNotFound
		}
		return r, e
	}
	after := gid
	switch q.Action {
	case domain.GuildCreate:
		if gid != 0 || !atGuildOffice(ctx, tx, session) {
			return result, domain.ErrForbidden
		}
		var taken bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM game_guilds WHERE world_id=$1 AND lower(name)=lower($2))`, world, q.Name).Scan(&taken); err != nil {
			return result, err
		}
		if taken {
			return result, domain.ErrAlreadyExists
		}
		c, e := debitGuildMesos(ctx, tx, session, domain.GuildCreateCost, q.OperationID, "guild_create")
		if e != nil {
			return result, e
		}
		if err = tx.QueryRowContext(ctx, `INSERT INTO game_guilds(world_id,name,leader_id) VALUES($1,$2,$3) RETURNING id`, world, q.Name, session.CharacterID).Scan(&after); err != nil {
			return result, err
		}
		_ = exec(`INSERT INTO game_guild_members(character_id,guild_id,rank) VALUES($1,$2,1)`, session.CharacterID, after)
		if err == nil {
			err = readGuildTag(ctx, tx, &c)
		}
		result.Character = &c
	case domain.GuildInvite:
		if gid == 0 || rank > domain.GuildJrMaster {
			return result, domain.ErrForbidden
		}
		inv := domain.GuildInvitation{GuildID: gid, GuildName: result.Before.Name, InviterID: session.CharacterID}
		e := tx.QueryRowContext(ctx, `SELECT c.id,c.name,(SELECT name FROM game_characters WHERE id=$3),(SELECT level FROM game_characters WHERE id=$3),(SELECT job FROM game_characters WHERE id=$3) FROM game_characters c WHERE lower(c.name)=lower($1) AND c.world_id=$2`, q.Name, world, session.CharacterID).Scan(&inv.TargetID, &inv.TargetName, &inv.InviterName, &inv.InviterLevel, &inv.InviterJob)
		if errors.Is(e, sql.ErrNoRows) {
			return result, domain.ErrNotFound
		}
		if e != nil {
			return result, e
		}
		if inv.TargetID == session.CharacterID {
			return result, domain.ErrInvalidInput
		}
		if other, _, e := guildFor(ctx, tx, inv.TargetID); e != nil || other != 0 {
			return result, errors.Join(e, domain.ErrAlreadyExists)
		}
		var pending bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM game_guild_invitations WHERE target_id=$1 AND expires_at>clock_timestamp())`, inv.TargetID).Scan(&pending); err != nil {
			return result, err
		}
		if pending {
			return result, domain.ErrGuildInvitePending
		}
		if err = tx.QueryRowContext(ctx, `INSERT INTO game_guild_invitations(target_id,guild_id,inviter_id,expires_at) VALUES($1,$2,$3,clock_timestamp()+interval '20 minutes')
		ON CONFLICT(target_id) DO UPDATE SET guild_id=EXCLUDED.guild_id,inviter_id=EXCLUDED.inviter_id,expires_at=EXCLUDED.expires_at RETURNING expires_at`, inv.TargetID, gid, session.CharacterID).Scan(&inv.ExpiresAt); err != nil {
			return result, err
		}
		result.TargetID, result.TargetName, result.Invitation = inv.TargetID, inv.TargetName, &inv
	case domain.GuildAccept, domain.GuildDeny:
		inv := domain.GuildInvitation{TargetID: session.CharacterID}
		e := tx.QueryRowContext(ctx, `DELETE FROM game_guild_invitations i USING game_guilds g, game_characters c WHERE i.target_id=$1 AND g.id=i.guild_id AND c.id=i.inviter_id AND g.world_id=$2 AND i.expires_at>clock_timestamp() AND ($3=0 OR i.guild_id=$3)
		RETURNING i.guild_id,g.name,i.inviter_id,c.name`, session.CharacterID, world, q.GuildID).Scan(&inv.GuildID, &inv.GuildName, &inv.InviterID, &inv.InviterName)
		if errors.Is(e, sql.ErrNoRows) {
			return result, domain.ErrNotFound
		}
		if e != nil {
			return result, e
		}
		result.Invitation = &inv
		if q.Action == domain.GuildDeny {
			after = 0
			break
		}
		if gid != 0 {
			return result, domain.ErrAlreadyExists
		}
		var members, capacity int
		if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM game_guild_members WHERE guild_id=$1),capacity FROM game_guilds WHERE id=$1`, inv.GuildID).Scan(&members, &capacity); err != nil {
			return result, err
		}
		if members >= domain.EffectiveGuildCapacity(capacity) {
			return result, domain.ErrGuildFull
		}
		after = inv.GuildID
		_ = exec(`INSERT INTO game_guild_members(character_id,guild_id,rank,alliance_rank) SELECT $1,$2,$3,CASE WHEN alliance_id IS NULL THEN 5 ELSE 3 END FROM game_guilds WHERE id=$2`, session.CharacterID, after, domain.GuildMember)
		_ = bump(after)
		result.TargetID = session.CharacterID
	case domain.GuildLeave:
		if gid == 0 {
			return result, domain.ErrNotFound
		}
		result.TargetID, result.TargetName = session.CharacterID, session.Character.Name
		after = 0
		if rank == domain.GuildMaster {
			if len(result.Before.Members) > 1 {
				return result, domain.ErrForbidden
			}
			_ = exec(`DELETE FROM game_guilds WHERE id=$1`, gid)
			result.Disbanded = true
		} else {
			_ = exec(`DELETE FROM game_guild_members WHERE character_id=$1`, session.CharacterID)
			_ = bump(gid)
			// After is the remaining guild, so the world keeps the other
			// members' live guild tags; the leaver is no longer in it.
			after = gid
		}
	case domain.GuildExpel:
		if gid == 0 || rank > domain.GuildJrMaster || q.TargetID == session.CharacterID {
			return result, domain.ErrForbidden
		}
		r, e := targetRank()
		if e != nil {
			return result, e
		}
		if r <= rank {
			return result, domain.ErrForbidden
		}
		_ = exec(`DELETE FROM game_guild_members WHERE character_id=$1`, q.TargetID)
		_ = bump(gid)
	case domain.GuildChangeRank:
		if gid == 0 || rank > domain.GuildJrMaster || q.Rank <= domain.GuildJrMaster && rank != domain.GuildMaster || q.TargetID == session.CharacterID {
			return result, domain.ErrForbidden
		}
		r, e := targetRank()
		if e != nil {
			return result, e
		}
		if r <= rank && rank != domain.GuildMaster || r == domain.GuildMaster {
			return result, domain.ErrForbidden
		}
		_ = exec(`UPDATE game_guild_members SET rank=$2 WHERE character_id=$1`, q.TargetID, q.Rank)
		_ = bump(gid)
	case domain.GuildTitles:
		if gid == 0 || rank != domain.GuildMaster {
			return result, domain.ErrForbidden
		}
		raw, e := json.Marshal(q.Titles[:])
		if e != nil {
			return result, e
		}
		_ = exec(`UPDATE game_guilds SET titles=ARRAY(SELECT jsonb_array_elements_text($2::jsonb)),revision=revision+1 WHERE id=$1`, gid, string(raw))
	case domain.GuildEmblem:
		if gid == 0 || rank != domain.GuildMaster || !atGuildOffice(ctx, tx, session) {
			return result, domain.ErrForbidden
		}
		c, e := debitGuildMesos(ctx, tx, session, domain.GuildEmblemCost, q.OperationID, "guild_emblem")
		if e != nil {
			return result, e
		}
		_ = exec(`UPDATE game_guilds SET logo_bg=$2,logo_bg_color=$3,logo=$4,logo_color=$5,revision=revision+1 WHERE id=$1`, gid, q.Emblem.BG, q.Emblem.BGColor, q.Emblem.Logo, q.Emblem.Color)
		if err == nil {
			err = readGuildTag(ctx, tx, &c)
		}
		result.Character = &c
	case domain.GuildNotice:
		if gid == 0 || rank > domain.GuildJrMaster {
			return result, domain.ErrForbidden
		}
		_ = exec(`UPDATE game_guilds SET notice=$2,revision=revision+1 WHERE id=$1`, gid, q.Notice)
	case domain.GuildLeader:
		if gid == 0 || rank != domain.GuildMaster || q.TargetID == session.CharacterID {
			return result, domain.ErrForbidden
		}
		if _, e := targetRank(); e != nil {
			return result, e
		}
		_ = exec(`UPDATE game_guild_members SET rank=2 WHERE character_id=$1`, session.CharacterID)
		_ = exec(`UPDATE game_guild_members SET rank=1 WHERE character_id=$1`, q.TargetID)
		_ = exec(`UPDATE game_guilds SET leader_id=$2,revision=revision+1 WHERE id=$1`, gid, q.TargetID)
	}
	if err != nil {
		return result, err
	}
	if handled, e := mutateGuildEconomy(ctx, tx, q, gid, rank, &result); handled {
		if e != nil {
			return result, e
		}
		if result.Disbanded {
			after = 0
		}
	}
	if after != 0 {
		if result.After, err = readGuildSnapshot(ctx, tx, after); err != nil {
			return result, err
		}
	}
	receipt := result
	receipt.Character = nil
	raw, err := json.Marshal(receipt)
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO game_guild_operations(character_id,operation_id,action,result) VALUES($1,$2,$3,$4)`, session.CharacterID, q.OperationID, string(q.Action), raw); err != nil {
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

// mutateGuildEconomy covers the guild office NPC (disband, capacity), skills
// and GP. It runs inside MutateGuild's locked transaction.
func mutateGuildEconomy(ctx context.Context, tx *sql.Tx, q domain.GuildRequest, gid int64, rank int, result *domain.GuildMutation) (bool, error) {
	session := q.Session
	g := result.Before
	switch q.Action {
	case domain.GuildDisband:
		if gid == 0 || rank != domain.GuildMaster || g.Alliance != 0 {
			return true, domain.ErrForbidden
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM game_guilds WHERE id=$1`, gid)
		result.Disbanded = true
		return true, err
	case domain.GuildCapacity:
		if gid == 0 || rank != domain.GuildMaster {
			return true, domain.ErrForbidden
		}
		// g.Capacity is already the effective capacity (readGuildSnapshot).
		limit := domain.GuildMesosCapacityLimit()
		if q.UseGP {
			limit = domain.GuildCapacityLimit()
		}
		next := g.Capacity + domain.GuildCapacityStep
		if next > limit {
			return true, domain.ErrGuildFull
		}
		if q.UseGP {
			if g.GP < domain.GuildCapacityGP {
				return true, domain.ErrInsufficient
			}
			if err := gainGuildGP(ctx, tx, gid, 0, -domain.GuildCapacityGP); err != nil {
				return true, err
			}
		} else {
			c, err := debitGuildMesos(ctx, tx, session, domain.GuildCapacityMesos, q.OperationID, "guild_capacity")
			if err != nil {
				return true, err
			}
			result.Character = &c
		}
		_, err := tx.ExecContext(ctx, `UPDATE game_guilds SET capacity=$2,revision=revision+1 WHERE id=$1`, gid, next)
		return true, err
	case domain.GuildSkillBuy, domain.GuildSkillRenew:
		if gid == 0 || rank > domain.GuildJrMaster {
			return true, domain.ErrForbidden
		}
		definition, err := content.GuildSkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), q.SkillID)
		if err != nil {
			return true, domain.ErrInvalidInput
		}
		current := guildSkillLevel(g, q.SkillID)
		var now int64
		if err = tx.QueryRowContext(ctx, `SELECT (extract(epoch FROM clock_timestamp())*1000)::bigint`).Scan(&now); err != nil {
			return true, err
		}
		next := current
		var cost int64
		var at content.GuildSkillLevel
		if q.Action == domain.GuildSkillBuy {
			if current.Level >= definition.MaxLevel {
				return true, domain.ErrForbidden
			}
			if at, err = definition.At(current.Level + 1); err != nil {
				return true, domain.ErrInvalidInput
			}
			if at.ReqGuildLevel <= 0 || at.ReqGuildLevel > domain.GuildLevelFor(g.GP) {
				return true, domain.ErrForbidden
			}
			cost = at.Price
			next.Level, next.Purchaser = at.Level, session.Character.Name
		} else {
			if current.Level <= 0 {
				return true, domain.ErrNotFound
			}
			if at, err = definition.At(current.Level); err != nil {
				return true, domain.ErrInvalidInput
			}
			// Legacy activateSkill: only an expired, periodic skill renews.
			if at.PeriodMinutes <= 0 || current.ExpiresAt > now || at.ReqGuildLevel < 0 {
				return true, domain.ErrForbidden
			}
			cost = at.ExtendPrice
		}
		next.Activator = session.Character.Name
		next.ExpiresAt = -1
		if at.PeriodMinutes > 0 {
			next.ExpiresAt = now + int64(at.PeriodMinutes)*60000
		}
		if cost > 0 {
			c, err := debitGuildMesos(ctx, tx, session, cost, q.OperationID, "guild_"+string(q.Action))
			if err != nil {
				return true, err
			}
			result.Character = &c
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO game_guild_skills(guild_id,skill_id,level,expires_at,purchaser,activator) VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(guild_id,skill_id) DO UPDATE SET level=EXCLUDED.level,expires_at=EXCLUDED.expires_at,purchaser=EXCLUDED.purchaser,activator=EXCLUDED.activator`, gid, next.SkillID, next.Level, next.ExpiresAt, next.Purchaser, next.Activator); err != nil {
			return true, err
		}
		result.Skill = &next
		if q.Action == domain.GuildSkillBuy {
			return true, gainGuildGP(ctx, tx, gid, 0, domain.GuildSkillGP)
		}
		_, err = tx.ExecContext(ctx, `UPDATE game_guilds SET revision=revision+1 WHERE id=$1`, gid)
		return true, err
	case domain.GuildGainGP:
		if gid == 0 {
			return true, domain.ErrNotFound
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO game_guild_gp_grants(character_id,period,guild_id,amount) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, session.CharacterID, q.Period, gid, q.Amount)
		if err != nil {
			return true, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return true, domain.ErrAlreadyExists
		}
		return true, gainGuildGP(ctx, tx, gid, session.CharacterID, q.Amount)
	}
	return false, nil
}

// atGuildOffice is the Legacy guild office (英雄之殿) rule. A character with an
// active dedicated QA grant is treated as present, because world warps are not
// enabled yet; the fee and every other rule still apply.
func atGuildOffice(ctx context.Context, tx *sql.Tx, s domain.CharacterSession) bool {
	if s.Character.MapID == domain.GuildOfficeMapID {
		return true
	}
	c := domain.Character{ID: s.CharacterID}
	if err := tx.QueryRowContext(ctx, `SELECT account_id,world_id,gm FROM game_characters WHERE id=$1`, s.CharacterID).Scan(&c.AccountID, &c.WorldID, &c.GM); err != nil {
		return false
	}
	grant, err := readQAGMGrant(ctx, tx, c.ID)
	if err != nil {
		return false
	}
	var now time.Time
	if tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now) != nil {
		return false
	}
	return validQAGMGrant(grant, c, now)
}
