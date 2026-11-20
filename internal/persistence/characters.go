package persistence

import (
	"context"
	"fmt"
	"golang.org/x/text/encoding/traditionalchinese"
	"maple-go-server/ent"
	"maple-go-server/ent/account"
	"maple-go-server/ent/character"
	"maple-go-server/ent/characterroster"
	"maple-go-server/ent/equipment"
	"maple-go-server/internal/domain"
	"regexp"
	"strings"
)

var charNamePattern = regexp.MustCompile(`^[A-Za-z0-9_\p{Han}]{2,12}$`)

func validCharacterName(name string) bool {
	if !charNamePattern.MatchString(name) {
		return false
	}
	encoded, err := traditionalchinese.Big5.NewEncoder().Bytes([]byte(name))
	return err == nil && len(encoded) <= 12
}
func (s *Store) CharacterNameAvailable(ctx context.Context, name string) (bool, error) {
	if !validCharacterName(name) {
		return false, domain.ErrInvalidInput
	}
	exists, err := s.client.Character.Query().Where(character.NameKeyEQ(strings.ToLower(name))).Exist(ctx)
	return !exists, err
}
func characterValue(c *ent.Character) domain.Character {
	return domain.Character{
		ID: c.ID, AccountID: c.AccountID, WorldID: c.WorldID, Name: c.Name, Gender: c.Gender, Skin: c.Skin, Face: c.Face, Hair: c.Hair,
		Level: c.Level, Job: c.Job, STR: c.Str, DEX: c.Dex, INT: c.IntStat, LUK: c.Luk, HP: c.Hp, MP: c.Mp, MaxHP: c.MaxHp, MaxMP: c.MaxMp, AP: c.Ap, SP: c.Sp, EXP: c.Exp, Fame: c.Fame, Mesos: c.Mesos, MapID: c.MapID, SpawnPoint: c.SpawnPoint, X: c.X, Y: c.Y, GM: c.Gm, Revision: c.Revision,
		Equipment: []domain.Equipment{},
	}
}
func equipmentValue(e *ent.Equipment) domain.Equipment {
	return domain.Equipment{ID: e.ID, ItemID: e.ItemID, Slot: e.Slot, STR: e.Str, DEX: e.Dex, INT: e.IntStat, LUK: e.Luk, HP: e.Hp, MP: e.Mp, WATK: e.Watk, MATK: e.Matk, WDEF: e.Wdef, MDEF: e.Mdef, ACC: e.Acc, AVOID: e.Avoid, Speed: e.Speed, Jump: e.Jump, UpgradeSlots: e.UpgradeSlots}
}
func readCharacter(ctx context.Context, client *ent.Client, id int64) (domain.Character, error) {
	c, err := client.Character.Get(ctx, id)
	if ent.IsNotFound(err) {
		return domain.Character{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Character{}, err
	}
	items, err := client.Equipment.Query().Where(equipment.CharacterIDEQ(id)).Order(ent.Asc(equipment.FieldSlot)).All(ctx)
	if err != nil {
		return domain.Character{}, err
	}
	value := characterValue(c)
	for _, item := range items {
		value.Equipment = append(value.Equipment, equipmentValue(item))
	}
	// Stackable bag rows are outside Ent until the inventory entity is generated; SQL matches SaveCharacter/meso path.
	bag, err := readInventoryItems(ctx, client, id)
	if err != nil {
		return domain.Character{}, err
	}
	value.Items = bag
	if err = applyCashMetadata(ctx, client, &value); err != nil {
		return domain.Character{}, err
	}
	value.Quests, err = readQuestStates(ctx, client, id)
	if err != nil {
		return domain.Character{}, err
	}
	value.QuestRecords, err = readQuestRecords(ctx, client, id)
	if err != nil {
		return domain.Character{}, err
	}
	if err = readSkillState(ctx, client, &value); err != nil {
		return domain.Character{}, err
	}
	if err = readInventorySlots(ctx, client, &value); err != nil {
		return domain.Character{}, err
	}
	if err = readIntrinsicPassives(ctx, client, &value); err != nil {
		return domain.Character{}, err
	}
	if err = readBaseVitals(ctx, client, &value); err != nil {
		return domain.Character{}, err
	}
	if err = readJobState(ctx, client, &value); err != nil {
		return domain.Character{}, err
	}
	return value, nil
}

// Load both persisted passive sources before deriving effective vitals.
func readIntrinsicPassives(ctx context.Context, q questQuerier, c *domain.Character) error {
	cards, err := readCharacterCards(ctx, q, c.AccountID, c.WorldID)
	if err != nil {
		return err
	}
	c.Cards = cards
	return readInnerAbilities(ctx, q, c)
}

func readInventoryItems(ctx context.Context, client *ent.Client, characterID int64) ([]domain.InventoryItem, error) {
	rows, err := client.QueryContext(ctx, `SELECT id, template_id, inv_type, slot, quantity, revision, expires_at FROM asset_items WHERE character_id=$1 ORDER BY inv_type, slot`, characterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.InventoryItem, 0, 4)
	for rows.Next() {
		var item domain.InventoryItem
		if err = rows.Scan(&item.ID, &item.TemplateID, &item.InvType, &item.Slot, &item.Quantity, &item.Revision, &item.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *Store) GetCharacter(ctx context.Context, id int64) (domain.Character, error) {
	return readCharacter(ctx, s.client, id)
}
func (s *Store) ListCharacters(ctx context.Context, accountID int64, worldID int) ([]domain.CharacterSummary, error) {
	if hit, ok := s.roster.Get(ctx, accountID, worldID); ok {
		s.cacheHits.Add(1)
		return rosterViews(hit), nil
	}
	key := fmt.Sprintf("%d:%d", accountID, worldID)
	value, err, _ := s.loads.Do(key, func() (any, error) {
		if hit, ok := s.roster.Get(ctx, accountID, worldID); ok {
			s.cacheHits.Add(1)
			return rosterViews(hit), nil
		}
		s.reads.Add(1)
		rows, err := s.client.CharacterRoster.Query().Where(characterroster.AccountIDEQ(accountID), characterroster.WorldIDEQ(worldID)).Order(ent.Asc(characterroster.FieldCharacterID)).Limit(domain.MaxCharacterSlots).All(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]domain.CharacterSummary, 0, len(rows))
		for _, row := range rows {
			out = append(out, domain.RosterView(row.Snapshot))
		}
		s.roster.Set(ctx, accountID, worldID, out)
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return value.([]domain.CharacterSummary), nil
}
func normalizeCreate(r *domain.CreateCharacterRequest) error {
	if !validCharacterName(r.Name) || r.AccountID <= 0 || r.WorldID < 0 || r.WorldID > 127 || (r.Gender != 0 && r.Gender != 1) || !validCreationSkin(r.Family, r.Skin) {
		return domain.ErrInvalidInput
	}
	race, ok := domain.CreationFamily(r.Family, r.SubJob)
	if !ok {
		return domain.ErrInvalidInput
	}
	if race.Family != 0 {
		if !validCreationLook(*r) {
			return domain.ErrInvalidInput
		}
		return normalizeOperation(r)
	}
	if r.Face == 0 {
		if r.Gender == 0 {
			r.Face = 20100
		} else {
			r.Face = 21700
		}
	}
	if r.Hair == 0 {
		if r.Gender == 0 {
			r.Hair = 30000
		} else {
			r.Hair = 31002
		}
	}
	if r.Top == 0 {
		if r.Gender == 0 {
			r.Top = 1040010
		} else {
			r.Top = 1041002
		}
	}
	if r.Bottom == 0 {
		if r.Gender == 0 {
			r.Bottom = 1060002
		} else {
			r.Bottom = 1061002
		}
	}
	if r.Shoes == 0 {
		r.Shoes = 1072005
	}
	if r.Weapon == 0 {
		r.Weapon = 1322005
	}
	// The Adventurer allowlist is taken from Legacy MakeCharInfo.img/000.
	// Category-only checks would allow nonexistent/custom assets or arbitrary gear.
	validLook := oneOf(r.Face, 20100, 20401, 20402) && oneOf(r.Hair, 30000, 30027, 30030) && oneOf(r.Top, 1040002, 1040006, 1040010) && oneOf(r.Bottom, 1060002, 1060006)
	if r.Gender == 1 {
		validLook = oneOf(r.Face, 21700, 21201, 21002) && oneOf(r.Hair, 31002, 31047, 31057) && oneOf(r.Top, 1041002, 1041006, 1041010, 1041011) && oneOf(r.Bottom, 1061002, 1061008)
	}
	if !validLook || !oneOf(r.Shoes, 1072001, 1072005, 1072037, 1072038) || !oneOf(r.Weapon, 1302000, 1312004, 1322005) {
		return domain.ErrInvalidInput
	}
	return normalizeOperation(r)
}

func normalizeOperation(r *domain.CreateCharacterRequest) error {
	if r.OperationID == "" {
		value, err := randomToken(16)
		if err != nil {
			return err
		}
		r.OperationID = value
	}
	if len(r.OperationID) > 128 {
		return domain.ErrInvalidInput
	}
	return nil
}

func oneOf(value int, allowed ...int) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
func (s *Store) CreateCharacter(ctx context.Context, request domain.CreateCharacterRequest) (domain.Character, error) {
	if err := normalizeCreate(&request); err != nil {
		return domain.Character{}, err
	}
	start, err := creationStart(request)
	if err != nil {
		return domain.Character{}, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return domain.Character{}, err
	}
	defer tx.Rollback()
	if request.Lease.WorldID != request.WorldID {
		return domain.Character{}, domain.ErrStaleOwner
	}
	if err = guardLease(ctx, tx, request.Lease); err != nil {
		return domain.Character{}, err
	}
	if request.LoginSession.AccountID != request.AccountID {
		return domain.Character{}, domain.ErrForbidden
	}
	if err = guardLogin(ctx, tx, request.LoginSession); err != nil {
		return domain.Character{}, err
	}
	// Serialize slots/name creation per account. Global name uniqueness is enforced by PG.
	a, err := tx.Account.Query().Where(account.IDEQ(request.AccountID)).ForUpdate().Only(ctx)
	if err != nil {
		return domain.Character{}, domain.ErrForbidden
	}
	if a.Gender != request.Gender && !domain.CreationGenderFree(request.Family) {
		return domain.Character{}, domain.ErrForbidden
	}
	previous, err := tx.Character.Query().Where(character.OperationIDEQ(request.OperationID)).Only(ctx)
	if err == nil {
		if previous.AccountID != request.AccountID || previous.WorldID != request.WorldID || previous.Name != request.Name {
			return domain.Character{}, domain.ErrForbidden
		}
		return readCharacter(ctx, tx.Client(), previous.ID)
	}
	if !ent.IsNotFound(err) {
		return domain.Character{}, err
	}
	count, err := tx.Character.Query().Where(character.AccountIDEQ(request.AccountID), character.WorldIDEQ(request.WorldID)).Count(ctx)
	if err != nil {
		return domain.Character{}, err
	}
	if count >= domain.CharacterSlots() {
		return domain.Character{}, domain.ErrInvalidInput
	}
	c, err := tx.Character.Create().SetAccountID(request.AccountID).SetWorldID(request.WorldID).SetName(request.Name).SetNameKey(strings.ToLower(request.Name)).SetOperationID(request.OperationID).SetGender(request.Gender).SetSkin(request.Skin).SetFace(request.Face).SetHair(request.Hair).SetMesos(1000).SetJob(start.race.Job).SetMapID(start.mapID).SetSpawnPoint(start.portal).SetX(start.x).SetY(start.y).Save(ctx)
	if ent.IsConstraintError(err) {
		return domain.Character{}, domain.ErrAlreadyExists
	}
	if err != nil {
		return domain.Character{}, err
	}
	if start.race.SubJob != 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE game_characters SET subcategory=$1 WHERE id=$2`, start.race.SubJob, c.ID); err != nil {
			return domain.Character{}, err
		}
	}
	for _, item := range []struct{ id, slot int }{{request.Top, -5}, {request.Bottom, -6}, {request.Shoes, -7}, {start.cape, -9}, {start.shield, -10}, {request.Weapon, -11}} {
		if item.id == 0 {
			continue // An overall occupies -5 and has no bottom; few families start with a cape.
		}
		create := tx.Equipment.Create().SetCharacterID(c.ID).SetItemID(item.id).SetSlot(item.slot)
		if stats, ok := start.gear[item.id]; ok {
			create.SetStr(stats.STR).SetDex(stats.DEX).SetIntStat(stats.INT).SetLuk(stats.LUK).SetHp(stats.HP).SetMp(stats.MP).SetWatk(stats.WATK).SetMatk(stats.MATK).SetWdef(stats.WDEF).SetMdef(stats.MDEF).SetAcc(stats.ACC).SetAvoid(stats.AVOID).SetSpeed(stats.Speed).SetJump(stats.Jump).SetUpgradeSlots(stats.UpgradeSlots)
		} else if item.slot == -11 {
			create.SetWatk(17)
		} else {
			create.SetWdef(3)
		}
		if _, err = create.Save(ctx); err != nil {
			return domain.Character{}, err
		}
	}
	// Starter USE: 5× red potion (2000000). Legacy Item.getType()==2 bundle; inventory type USE=2.
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_items(character_id, inv_type, slot, template_id, quantity) VALUES($1,2,1,2000000,5)`, c.ID); err != nil {
		return domain.Character{}, err
	}
	result, err := readCharacter(ctx, tx.Client(), c.ID)
	if err != nil {
		return domain.Character{}, err
	}
	if _, err = tx.CharacterRoster.Create().SetAccountID(c.AccountID).SetWorldID(c.WorldID).SetCharacterID(c.ID).SetSnapshot(domain.RosterView(result)).Save(ctx); err != nil {
		return domain.Character{}, err
	}
	if _, err = tx.Outbox.Create().SetEventType("roster.invalidate").SetAccountID(c.AccountID).SetWorldID(c.WorldID).Save(ctx); err != nil {
		return domain.Character{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.Character{}, err
	}
	s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	return result, nil
}
