package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

// familyGear is the first choice of each MakeCharInfo list for a family and
// gender, plus Legacy CreateChar's fixed shields (Mercedes 1352000, Demon
// MakeCharInfo/3001 slot 6). A zero item leaves that worn slot empty.
type familyGear struct {
	face, hair, mark                   int
	top, bottom, shoes, weapon, shield int
}

func qaFamilyGear(family, gender int) (familyGear, bool) {
	switch family {
	case 2002:
		return [2]familyGear{
			{face: 20549, hair: 33453, top: 1050192, shoes: 1072519, weapon: 1522038, shield: 1352000},
			{face: 21547, hair: 34423, top: 1051237, shoes: 1072519, weapon: 1522038, shield: 1352000},
		}[gender], true
	case 3001:
		return [2]familyGear{
			{face: 20248, hair: 33531, mark: 1012276, top: 1050191, shoes: 1072518, weapon: 1322123, shield: 1099001},
			{face: 21246, hair: 34411, mark: 1012276, top: 1051236, shoes: 1072518, weapon: 1322123, shield: 1099001},
		}[gender], true
	}
	looks, ok := creationLooks[family]
	if !ok {
		return familyGear{}, false // Phantom: v145 has no MakeCharInfo look
	}
	l := looks[gender]
	g := familyGear{face: l.face[0], hair: l.hair[0], top: l.top[0], shoes: l.shoes[0], weapon: l.weapon[0]}
	if l.bottom != nil {
		g.bottom = l.bottom[0]
	}
	return g, true
}

func qaFamilyAudit(c domain.Character) []byte {
	raw, _ := json.Marshal(struct {
		Job, SubJob, Face, Hair, DemonMark, SP int
		SPBooks                                domain.SPBookTable
		Equipment                              []domain.Equipment
	}{c.Job, c.SubJob, c.Face, c.Hair, c.DemonMark, c.SP, c.SPBooks, c.Equipment})
	return raw
}

func (s *Store) ApplyQAFamily(ctx context.Context, r domain.QAFamilyRequest) (domain.QAFamilyResult, error) {
	if !validGMOperation(r.OperationID) || r.AccountID < 1 || r.CharacterID < 1 || !domain.ValidJob(r.Job) || len(strings.TrimSpace(r.Actor)) < 1 || len(r.Actor) > 128 || len(strings.TrimSpace(r.Reason)) < 1 || len(r.Reason) > 512 {
		return domain.QAFamilyResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.QAFamilyResult{}, err
	}
	defer tx.Rollback()
	// Account before character, the order every world authority uses.
	var account int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM auth_accounts WHERE id=$1 FOR UPDATE`, r.AccountID).Scan(&account); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = domain.ErrNotFound
		}
		return domain.QAFamilyResult{}, err
	}
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM ops_account_sessions WHERE account_id=$1 FOR UPDATE`, r.AccountID).Scan(&state); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.QAFamilyResult{}, err
	}
	if state == "world" || state == "transfer" || state == "cashshop" {
		return domain.QAFamilyResult{}, domain.ErrAlreadyExists
	}
	var owner int64
	err = tx.QueryRowContext(ctx, `SELECT account_id FROM game_characters WHERE id=$1 FOR UPDATE`, r.CharacterID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || err == nil && owner != r.AccountID {
		return domain.QAFamilyResult{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.QAFamilyResult{}, err
	}
	var priorCharacter int64
	var priorJob int
	err = tx.QueryRowContext(ctx, `SELECT character_id,job FROM ops_qa_family_changes WHERE operation_id=$1`, r.OperationID).Scan(&priorCharacter, &priorJob)
	if err == nil {
		if priorCharacter != r.CharacterID || priorJob != r.Job {
			return domain.QAFamilyResult{}, domain.ErrForbidden
		}
		c, e := readCharacterSQL(ctx, tx, r.CharacterID)
		return domain.QAFamilyResult{Character: c, Replayed: true}, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.QAFamilyResult{}, err
	}
	c, err := readCharacterSQL(ctx, tx, r.CharacterID)
	if err != nil {
		return domain.QAFamilyResult{}, err
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return domain.QAFamilyResult{}, err
	}
	grant, err := readQAGMGrant(ctx, tx, c.ID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !validQAGMGrant(grant, c, now) {
		return domain.QAFamilyResult{}, domain.ErrForbidden
	}
	if err != nil {
		return domain.QAFamilyResult{}, err
	}
	before := qaFamilyAudit(c)
	if gear, ok := qaFamilyGear(domain.JobFamily(r.Job), c.Gender); ok {
		if err = swapFamilyGear(ctx, tx, c, gear); err != nil {
			return domain.QAFamilyResult{}, err
		}
		c.Face, c.Hair, c.DemonMark = gear.face, gear.hair, gear.mark
	} else {
		c.DemonMark = 0
	}
	c.SetJob(r.Job)
	c.SubJob = domain.SubJobFor(r.Job)
	if _, err = tx.ExecContext(ctx, `UPDATE game_characters SET job=$1,face=$2,hair=$3,sp=$4,demon_mark=$5 WHERE id=$6`, c.Job, c.Face, c.Hair, c.SP, c.DemonMark, c.ID); err != nil {
		return domain.QAFamilyResult{}, err
	}
	if err = saveJobState(ctx, tx, c); err != nil {
		return domain.QAFamilyResult{}, err
	}
	if c, err = readCharacterSQL(ctx, tx, r.CharacterID); err != nil {
		return domain.QAFamilyResult{}, err
	}
	if err = commitInventoryState(ctx, tx, &c); err != nil {
		return domain.QAFamilyResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ops_qa_family_changes(operation_id,character_id,account_id,actor,reason,job,grant_revision,before_state,after_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, r.OperationID, c.ID, c.AccountID, r.Actor, r.Reason, r.Job, grant.Revision, before, qaFamilyAudit(c)); err != nil {
		return domain.QAFamilyResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.QAFamilyResult{}, err
	}
	s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	return domain.QAFamilyResult{Character: c}, nil
}

// swapFamilyGear moves whatever is worn in the starter slots into free EQUIP
// bag slots (nothing is deleted) and wears the family's WZ starter items.
func swapFamilyGear(ctx context.Context, tx *sql.Tx, c domain.Character, g familyGear) error {
	used := map[int]bool{}
	for _, e := range c.Equipment {
		if e.Slot > 0 {
			used[e.Slot] = true
		}
	}
	next := 1
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	for _, item := range []struct{ id, slot int }{{g.top, -5}, {g.bottom, -6}, {g.shoes, -7}, {g.shield, -10}, {g.weapon, -11}} {
		for _, e := range c.Equipment {
			if e.Slot != item.slot {
				continue
			}
			for ; next <= c.Capacity(1) && used[next]; next++ {
			}
			if next > c.Capacity(1) {
				return domain.ErrInsufficient
			}
			if _, err := tx.ExecContext(ctx, `UPDATE asset_equipment SET slot=$1 WHERE id=$2`, next, e.ID); err != nil {
				return err
			}
			used[next] = true
		}
		if item.id == 0 {
			continue
		}
		def, err := content.ItemDefinition(root, item.id)
		if err != nil {
			return err
		}
		e := def.Equipment
		if _, err = tx.ExecContext(ctx, `INSERT INTO asset_equipment(character_id,item_id,slot,str,dex,int_stat,luk,hp,mp,watk,matk,wdef,mdef,acc,avoid,speed,jump,upgrade_slots) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
			c.ID, item.id, item.slot, e.STR, e.DEX, e.INT, e.LUK, e.HP, e.MP, e.WATK, e.MATK, e.WDEF, e.MDEF, e.ACC, e.AVOID, e.Speed, e.Jump, e.UpgradeSlots); err != nil {
			return err
		}
	}
	return nil
}
