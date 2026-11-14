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

var _ domain.PetRepository = (*Store)(nil)

// readPets attaches pet state for CASH serials held in this character's
// inventory. A summon owned by another character is shown as resting.
func readPets(ctx context.Context, q txQuery, c *domain.Character) error {
	rows, err := q.QueryContext(ctx, `SELECT p.cash_id,p.template_id,ci.slot,p.name,p.level,p.closeness,p.fullness,p.flags,p.skill_id,ci.expires_at,
	CASE WHEN p.summoned_by=$1 THEN p.summon_index+1 ELSE 0 END,p.revision
	FROM asset_pets p JOIN asset_cash_items ci ON ci.id=p.cash_id
	WHERE ci.character_id=$1 AND ci.state='inventory' AND ci.inv_type=5 ORDER BY ci.slot`, c.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	c.Pets = nil
	for rows.Next() {
		var p domain.Pet
		if err = rows.Scan(&p.CashID, &p.TemplateID, &p.Slot, &p.Name, &p.Level, &p.Closeness, &p.Fullness, &p.Flags, &p.SkillID, &p.ExpiresAt, &p.Summon, &p.Revision); err != nil {
			return err
		}
		p.X, p.Y = c.X, c.Y-12
		c.Pets = append(c.Pets, p)
	}
	return rows.Err()
}

func validPetName(name string) bool {
	if strings.TrimSpace(name) != name || name == "" || strings.ContainsAny(name, "\x00\r\n") {
		return false
	}
	p, err := traditionalchinese.Big5.NewEncoder().Bytes([]byte(name))
	return err == nil && len(p) <= 12 && len([]rune(name)) <= 12
}

func validPetOperation(id string) bool { return validGMOperation(id) }

// lockPetOwner serializes all pet commands of one owned character and returns
// its current pets. Lock order matches item use: lease, session, character.
func lockPetOwner(ctx context.Context, tx *sql.Tx, s domain.CharacterSession) (domain.Character, error) {
	if err := guardBuddyOwner(ctx, tx, s); err != nil {
		return domain.Character{}, err
	}
	c := domain.Character{ID: s.CharacterID, X: s.Character.X, Y: s.Character.Y}
	err := tx.QueryRowContext(ctx, `SELECT account_id,world_id FROM game_characters WHERE id=$1 FOR UPDATE`, s.CharacterID).Scan(&c.AccountID, &c.WorldID)
	if errors.Is(err, sql.ErrNoRows) {
		return c, domain.ErrStaleOwner
	}
	if err != nil {
		return c, err
	}
	if c.AccountID != s.AccountID || c.WorldID != s.Lease.WorldID {
		return c, domain.ErrForbidden
	}
	return c, readPets(ctx, tx, &c)
}

// petReceipt reports a replayed operation; the replay never re-applies.
func petReceipt(ctx context.Context, tx *sql.Tx, characterID int64, operationID string) (domain.PetResult, bool, error) {
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT result FROM asset_pet_commands WHERE character_id=$1 AND operation_id=$2`, characterID, operationID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PetResult{}, false, nil
	}
	if err != nil {
		return domain.PetResult{}, false, err
	}
	var out domain.PetResult
	return out, true, json.Unmarshal(raw, &out)
}

func writePetReceipt(ctx context.Context, tx *sql.Tx, characterID int64, operationID, kind string, result domain.PetResult) error {
	raw, err := json.Marshal(result)
	if err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO asset_pet_commands(character_id,operation_id,kind,result) VALUES($1,$2,$3,$4)`, characterID, operationID, kind, raw)
	}
	return err
}

// applyPetChanges clears every moved summon before assigning new indexes so
// the partial unique (owner,index) never sees an intermediate duplicate.
func applyPetChanges(ctx context.Context, tx *sql.Tx, owner int64, changes []domain.PetChange) error {
	for _, ch := range changes {
		if ch.Before.Summon != ch.After.Summon {
			if _, err := tx.ExecContext(ctx, `UPDATE asset_pets SET summoned_by=NULL,summon_index=NULL WHERE cash_id=$1`, ch.After.CashID); err != nil {
				return err
			}
		}
	}
	for _, ch := range changes {
		var by, index any
		if ch.After.Summoned() {
			by, index = owner, ch.After.Index()
		}
		if _, err := tx.ExecContext(ctx, `UPDATE asset_pets SET summoned_by=$2,summon_index=$3,level=$4,closeness=$5,fullness=$6,revision=revision+1,updated_at=clock_timestamp() WHERE cash_id=$1`, ch.After.CashID, by, index, ch.After.Level, ch.After.Closeness, ch.After.Fullness); err != nil {
			return err
		}
	}
	return nil
}

func finishPetChanges(c *domain.Character, changes []domain.PetChange) domain.PetResult {
	for i := range changes {
		changes[i].After.Revision++
		c.ReplacePet(changes[i].After)
	}
	return domain.PetResult{Pets: c.Pets, Changes: changes}
}

func petBySlot(c domain.Character, slot int) (domain.Pet, bool) {
	for _, p := range c.Pets {
		if p.Slot == slot {
			return p, true
		}
	}
	return domain.Pet{}, false
}

// planSummon follows Legacy MapleCharacter.spawnPet: a summoned pet is
// recalled; a fourth replaces index 0; "lead" shifts the others right.
func planSummon(c domain.Character, slot int, lead bool, now time.Time) ([]domain.PetChange, error) {
	target, ok := petBySlot(c, slot)
	if !ok {
		return nil, domain.ErrNotFound
	}
	if target.Summoned() {
		after := target
		after.Summon = 0
		return []domain.PetChange{{Before: target, After: after, Recalled: true}}, nil
	}
	if target.Expired(now) {
		return nil, domain.ErrInvalidInput
	}
	summoned := c.SummonedPets()
	var changes []domain.PetChange
	index := len(summoned)
	switch {
	case len(summoned) == domain.MaxSummonedPets:
		first := summoned[0]
		after := first
		after.Summon = 0
		changes = append(changes, domain.PetChange{Before: first, After: after, Recalled: true})
		index = 0
	case lead:
		for i := len(summoned) - 1; i >= 0; i-- {
			after := summoned[i]
			after.Summon = i + 2
			changes = append(changes, domain.PetChange{Before: summoned[i], After: after})
		}
		index = 0
	default:
		used := map[int]bool{}
		for _, p := range summoned {
			used[p.Index()] = true
		}
		for index = 0; used[index]; index++ {
		}
	}
	after := target
	after.Summon = index + 1
	return append(changes, domain.PetChange{Before: target, After: after}), nil
}

func (s *Store) SummonPet(ctx context.Context, r domain.PetSummonRequest) (domain.PetResult, error) {
	if !validPetOperation(r.OperationID) || r.Slot < 1 || r.Slot > domain.MaxInventorySlots {
		return domain.PetResult{}, domain.ErrInvalidInput
	}
	if r.Session.Character.MapID == domain.FreeMarketMapID {
		return domain.PetResult{}, domain.ErrForbidden
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.PetResult{}, err
	}
	defer tx.Rollback()
	c, err := lockPetOwner(ctx, tx, r.Session)
	if err != nil {
		return domain.PetResult{}, err
	}
	if _, replay, err := petReceipt(ctx, tx, c.ID, r.OperationID); err != nil || replay {
		return domain.PetResult{}, errors.Join(err, domain.ErrAlreadyExists)
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return domain.PetResult{}, err
	}
	changes, err := planSummon(c, r.Slot, r.Lead, now)
	if err != nil {
		return domain.PetResult{}, err
	}
	if err = applyPetChanges(ctx, tx, c.ID, changes); err != nil {
		return domain.PetResult{}, err
	}
	result := finishPetChanges(&c, changes)
	result.Success = true
	if err = writePetReceipt(ctx, tx, c.ID, r.OperationID, "summon", result); err != nil {
		return domain.PetResult{}, err
	}
	return result, tx.Commit()
}

// gainCloseness applies a closeness delta and reports a level-up.
func gainCloseness(p domain.Pet, delta int) (domain.Pet, bool) {
	p.Closeness = max(0, min(domain.PetMaxCloseness, p.Closeness+delta))
	level := domain.PetLevelFor(p.Closeness)
	up := level > p.Level
	p.Level = level
	return p, up
}

func (s *Store) CommandPet(ctx context.Context, r domain.PetCommandRequest) (domain.PetResult, error) {
	if !validPetOperation(r.OperationID) || r.Index < 0 || r.Index >= domain.MaxSummonedPets || r.Increase < 1 || r.Increase > 100 {
		return domain.PetResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.PetResult{}, err
	}
	defer tx.Rollback()
	c, err := lockPetOwner(ctx, tx, r.Session)
	if err != nil {
		return domain.PetResult{}, err
	}
	if _, replay, err := petReceipt(ctx, tx, c.ID, r.OperationID); err != nil || replay {
		return domain.PetResult{}, errors.Join(err, domain.ErrAlreadyExists)
	}
	pet, ok := c.PetAt(r.Index)
	if !ok {
		return domain.PetResult{}, domain.ErrNotFound
	}
	after, up := gainCloseness(pet, r.Increase)
	changes := []domain.PetChange{{Before: pet, After: after, LevelUp: up}}
	if err = applyPetChanges(ctx, tx, c.ID, changes); err != nil {
		return domain.PetResult{}, err
	}
	result := finishPetChanges(&c, changes)
	result.Success = true
	if err = writePetReceipt(ctx, tx, c.ID, r.OperationID, "command", result); err != nil {
		return domain.PetResult{}, err
	}
	return result, tx.Commit()
}

// DecayPets is one Legacy hunger pass: expiry recalls (reason 2); hunger at
// or below 5 resets fullness to 15 and recalls (reason 1).
func (s *Store) DecayPets(ctx context.Context, r domain.PetDecayRequest) (domain.PetResult, error) {
	if !validPetOperation(r.OperationID) || r.Now.IsZero() {
		return domain.PetResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.PetResult{}, err
	}
	defer tx.Rollback()
	c, err := lockPetOwner(ctx, tx, r.Session)
	if err != nil {
		return domain.PetResult{}, err
	}
	if _, replay, err := petReceipt(ctx, tx, c.ID, r.OperationID); err != nil || replay {
		return domain.PetResult{}, errors.Join(err, domain.ErrAlreadyExists)
	}
	var changes []domain.PetChange
	for _, pet := range c.SummonedPets() {
		after := pet
		switch hunger := r.Hunger[pet.CashID]; {
		case pet.Expired(r.Now):
			after.Summon = 0
			changes = append(changes, domain.PetChange{Before: pet, After: after, Reason: 2, Recalled: true})
		case hunger > 0 && pet.Fullness-hunger <= 5:
			after.Summon, after.Fullness = 0, 15
			changes = append(changes, domain.PetChange{Before: pet, After: after, Reason: 1, Recalled: true})
		case hunger > 0:
			after.Fullness -= hunger
			changes = append(changes, domain.PetChange{Before: pet, After: after})
		}
	}
	if len(changes) == 0 {
		return domain.PetResult{Pets: c.Pets}, nil
	}
	if err = applyPetChanges(ctx, tx, c.ID, changes); err != nil {
		return domain.PetResult{}, err
	}
	result := finishPetChanges(&c, changes)
	result.Success = true
	if err = writePetReceipt(ctx, tx, c.ID, r.OperationID, "decay", result); err != nil {
		return domain.PetResult{}, err
	}
	return result, tx.Commit()
}

// FeedPet follows Legacy PetHandler.PetFood: the first summoned pet below
// full fullness eats (else index 0); one food is consumed in either case.
func (s *Store) FeedPet(ctx context.Context, r domain.PetFeedRequest) (domain.PetResult, error) {
	session := r.Session
	if !validPetOperation(r.OperationID) || r.Slot < 1 || r.Slot > domain.MaxInventorySlots || !domain.IsPetFood(r.TemplateID) {
		return domain.PetResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.PetResult{}, err
	}
	defer tx.Rollback()
	if _, err = lockPetOwner(ctx, tx, session); err != nil {
		return domain.PetResult{}, err
	}
	if _, replay, err := petReceipt(ctx, tx, session.CharacterID, r.OperationID); err != nil || replay {
		return domain.PetResult{}, errors.Join(err, domain.ErrAlreadyExists)
	}
	value, err := readCharacterSQL(ctx, tx, session.CharacterID)
	if err != nil {
		return domain.PetResult{}, err
	}
	if value.Revision != session.Character.Revision {
		return domain.PetResult{}, domain.ErrStaleOwner
	}
	value.X, value.Y = session.Character.X, session.Character.Y
	summoned := value.SummonedPets()
	if len(summoned) == 0 {
		return domain.PetResult{}, domain.ErrNotFound
	}
	pet := summoned[0]
	for _, p := range summoned {
		if p.Fullness < domain.PetMaxFullness {
			pet = p
			break
		}
	}
	index := -1
	for i, item := range value.Items {
		if item.InvType == 2 && item.Slot == r.Slot && item.TemplateID == r.TemplateID {
			index = i
			break
		}
	}
	if index < 0 {
		return domain.PetResult{}, domain.ErrNotFound
	}
	item := value.Items[index]
	if err = guardItemExpiry(ctx, tx, item.ExpiresAt); err != nil {
		return domain.PetResult{}, err
	}
	if item.Quantity <= 1 {
		_, err = tx.ExecContext(ctx, `DELETE FROM asset_items WHERE id=$1`, item.ID)
		value.Items = append(value.Items[:index], value.Items[index+1:]...)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE asset_items SET quantity=quantity-1,revision=revision+1 WHERE id=$1`, item.ID)
		value.Items[index].Quantity--
		value.Items[index].Revision++
	}
	if err != nil {
		return domain.PetResult{}, err
	}
	hungry := pet.Fullness < domain.PetMaxFullness
	after, up := pet, false
	switch {
	case hungry:
		after.Fullness = min(domain.PetMaxFullness, pet.Fullness+domain.PetFoodFullness)
		if r.GainCloseness && pet.Closeness < domain.PetMaxCloseness {
			after, up = gainCloseness(after, 1)
		}
	case r.GainCloseness:
		after.Closeness = max(0, pet.Closeness-1)
		after.Level = domain.PetLevelFor(after.Closeness)
	}
	changes := []domain.PetChange{{Before: pet, After: after, LevelUp: up}}
	if err = applyPetChanges(ctx, tx, value.ID, changes); err != nil {
		return domain.PetResult{}, err
	}
	value.Revision++
	if _, err = tx.ExecContext(ctx, `UPDATE game_characters SET revision=$1 WHERE id=$2`, value.Revision, value.ID); err != nil {
		return domain.PetResult{}, err
	}
	if err = syncCashLocations(ctx, tx, value); err != nil {
		return domain.PetResult{}, err
	}
	raw, err := marshalRoster(value)
	if err != nil {
		return domain.PetResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE game_character_roster SET snapshot=$1,revision=$2 WHERE character_id=$3`, raw, value.Revision, value.ID); err != nil {
		return domain.PetResult{}, err
	}
	result := finishPetChanges(&value, changes)
	result.Success = hungry
	result.Character = &value
	receipt := result
	receipt.Character = nil
	if err = writePetReceipt(ctx, tx, value.ID, r.OperationID, "feed", receipt); err != nil {
		return domain.PetResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.PetResult{}, err
	}
	s.roster.Invalidate(ctx, value.AccountID, value.WorldID)
	return result, nil
}

// GrantPet places a new pet in the first free CASH slot of an offline
// character. It is the operator/QA path; live inventories are never edited.
func (s *Store) GrantPet(ctx context.Context, r domain.PetGrantRequest) (domain.Pet, error) {
	if !validPetOperation(r.OperationID) || r.AccountID < 1 || r.CharacterID < 1 || !domain.IsPetTemplate(r.TemplateID) || !validPetName(r.Name) || r.PeriodDays < 0 || r.PeriodDays > 3650 {
		return domain.Pet{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Pet{}, err
	}
	defer tx.Rollback()
	// Account before character, the order every world authority uses.
	var account int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM auth_accounts WHERE id=$1 FOR UPDATE`, r.AccountID).Scan(&account); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = domain.ErrNotFound
		}
		return domain.Pet{}, err
	}
	var state string
	err = tx.QueryRowContext(ctx, `SELECT state FROM ops_account_sessions WHERE account_id=$1 FOR UPDATE`, r.AccountID).Scan(&state)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.Pet{}, err
	}
	if state == "world" || state == "transfer" || state == "cashshop" {
		return domain.Pet{}, domain.ErrAlreadyExists
	}
	var owner int64
	err = tx.QueryRowContext(ctx, `SELECT account_id FROM game_characters WHERE id=$1 FOR UPDATE`, r.CharacterID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || err == nil && owner != r.AccountID {
		return domain.Pet{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Pet{}, err
	}
	value, err := readCharacterSQL(ctx, tx, r.CharacterID)
	if err != nil {
		return domain.Pet{}, err
	}
	if prior, replay, err := petReceipt(ctx, tx, value.ID, r.OperationID); err != nil || replay {
		if err == nil && len(prior.Pets) == 1 {
			return prior.Pets[0], nil
		}
		return domain.Pet{}, errors.Join(err, domain.ErrAlreadyExists)
	}
	used := map[int]bool{}
	for _, item := range value.Items {
		if item.InvType == 5 {
			used[item.Slot] = true
		}
	}
	slot := 1
	for ; slot <= value.Capacity(5) && used[slot]; slot++ {
	}
	if slot > value.Capacity(5) {
		return domain.Pet{}, domain.ErrInsufficient
	}
	pet := domain.Pet{TemplateID: r.TemplateID, Slot: slot, Name: r.Name, Level: 1, Fullness: domain.PetMaxFullness, Revision: 1}
	if r.PeriodDays > 0 {
		var expires int64
		if err = tx.QueryRowContext(ctx, `SELECT (extract(epoch FROM clock_timestamp()+make_interval(days=>$1))*1000)::bigint`, r.PeriodDays).Scan(&expires); err != nil {
			return domain.Pet{}, err
		}
		pet.ExpiresAt = expires
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,character_id,world_id,sn,template_id,quantity,inv_type,slot,expires_at,state) VALUES($1,$2,$3,0,$4,1,5,$5,$6,'inventory') RETURNING id`, value.AccountID, value.ID, value.WorldID, pet.TemplateID, slot, pet.ExpiresAt).Scan(&pet.CashID); err != nil {
		return domain.Pet{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,5,$2,$3,1)`, value.ID, slot, pet.TemplateID); err != nil {
		return domain.Pet{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_pets(cash_id,template_id,name) VALUES($1,$2,$3)`, pet.CashID, pet.TemplateID, pet.Name); err != nil {
		return domain.Pet{}, err
	}
	if err = writePetReceipt(ctx, tx, value.ID, r.OperationID, "grant", domain.PetResult{Pets: []domain.Pet{pet}, Success: true}); err != nil {
		return domain.Pet{}, err
	}
	return pet, tx.Commit()
}

// petLifeDays is the WZ lifetime used when a pet product has no period.
func petLifeDays(templateID, period int) int {
	if !domain.IsPetTemplate(templateID) || period > 0 {
		return 0
	}
	definition, err := content.PetDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), templateID)
	if err != nil {
		return 0
	}
	return definition.LifeDays
}

// createCashPet attaches fresh pet state to a purchased CASH serial, named
// after String.wz like Legacy MaplePet.createPet.
func createCashPet(ctx context.Context, tx *sql.Tx, cashID int64, templateID int) error {
	name := "Pet"
	if definition, err := content.PetDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), templateID); err == nil && validPetName(definition.Name) {
		name = definition.Name
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO asset_pets(cash_id,template_id,name) VALUES($1,$2,$3)`, cashID, templateID, name)
	return err
}

func (s *Store) ListPets(ctx context.Context, accountID, characterID int64) ([]domain.Pet, error) {
	c := domain.Character{ID: characterID}
	if err := s.db.QueryRowContext(ctx, `SELECT x,y FROM game_characters WHERE id=$1 AND account_id=$2`, characterID, accountID).Scan(&c.X, &c.Y); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = domain.ErrNotFound
		}
		return nil, err
	}
	if err := readPets(ctx, s.db, &c); err != nil {
		return nil, err
	}
	return c.Pets, nil
}
