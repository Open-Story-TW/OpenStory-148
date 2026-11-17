package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

var _ domain.ScriptRepository = (*Store)(nil)
var scriptLogZone = time.FixedZone("TMS-UTC+08", 8*60*60)

// ScriptLogWindow uses calendar periods in server UTC+08. Weeks begin Monday.
// Legacy rolling-24h/7d logs must be explicitly mapped when scripts migrate;
// they are not silently treated as calendar-day/week counters.
func ScriptLogWindow(now time.Time, period domain.LogPeriod) (time.Time, error) {
	now = now.In(scriptLogZone)
	y, m, d := now.Date()
	h := 0
	switch period {
	case domain.LogPermanent:
		return time.Unix(0, 0).UTC(), nil
	case domain.LogHourly:
		h = now.Hour()
	case domain.LogDaily:
	case domain.LogWeekly:
		d -= (int(now.Weekday()) + 6) % 7
	case domain.LogMonthly:
		d = 1
	case domain.LogYearly:
		m = time.January
		d = 1
	default:
		return time.Time{}, domain.ErrInvalidInput
	}
	return time.Date(y, m, d, h, 0, 0, 0, scriptLogZone).UTC(), nil
}

type storedLogKey struct {
	domain.ScriptLogKey
	owner int64
	world int
	start time.Time
}

func logKey(session domain.CharacterSession, key domain.ScriptLogKey, now time.Time) (storedLogKey, error) {
	if strings.TrimSpace(key.Name) == "" || utf8.RuneCountInString(key.Name) > 128 || strings.ContainsRune(key.Name, '\x00') {
		return storedLogKey{}, domain.ErrInvalidInput
	}
	start, err := ScriptLogWindow(now, key.Period)
	if err != nil {
		return storedLogKey{}, err
	}
	owner := int64(0)
	switch key.Scope {
	case domain.LogCharacter:
		owner = session.CharacterID
	case domain.LogAccount:
		owner = session.AccountID
	case domain.LogWorld:
		owner = int64(session.Lease.WorldID)
	default:
		return storedLogKey{}, domain.ErrInvalidInput
	}
	return storedLogKey{ScriptLogKey: key, owner: owner, world: session.Lease.WorldID, start: start}, nil
}

func (k storedLogKey) args() []any {
	return []any{k.Scope, k.owner, k.world, k.Name, k.Period, k.start}
}
func (k storedLogKey) order() string {
	return fmt.Sprintf("%s/%020d/%03d/%s/%s", k.Scope, k.owner, k.world, k.Name, k.Period)
}

func readLog(ctx context.Context, tx *sql.Tx, k storedLogKey, lock bool) (domain.ScriptLogValue, error) {
	value := domain.ScriptLogValue{Key: k.ScriptLogKey, WindowStart: k.start}
	query := `SELECT value,revision FROM game_script_logs WHERE scope=$1 AND owner_id=$2 AND world_id=$3 AND name=$4 AND period=$5 AND window_start=$6`
	if lock {
		query += " FOR UPDATE"
	}
	err := tx.QueryRowContext(ctx, query, k.args()...).Scan(&value.Value, &value.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return value, nil
	}
	return value, err
}

func (s *Store) ReadScriptLogs(ctx context.Context, session domain.CharacterSession, keys []domain.ScriptLogKey) ([]domain.ScriptLogValue, error) {
	if len(keys) > 64 {
		return nil, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = lockInventoryCharacter(ctx, tx, session); err != nil {
		return nil, err
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return nil, err
	}
	values := make([]domain.ScriptLogValue, 0, len(keys))
	for _, key := range keys {
		k, err := logKey(session, key, now)
		if err != nil {
			return nil, err
		}
		v, err := readLog(ctx, tx, k, false)
		if err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, nil
}

func scriptExchangeHash(r domain.ScriptExchange) string {
	// Bind idempotency to the actor, but not ephemeral live revision/position.
	value := struct {
		Account int64
		World   int
		Command domain.ScriptExchange
	}{r.Session.AccountID, r.Session.Lease.WorldID, r}
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (s *Store) ExecuteScriptExchange(ctx context.Context, r domain.ScriptExchange) (domain.ScriptExchangeResult, error) {
	if len(r.OperationID) < 16 || len(r.OperationID) > 128 || len(r.Logs) > 32 || len(r.Items) > 32 || len(r.Drops) > 8 || len(r.QuestData) > 32 || len(r.Quests) > 32 || r.EXP < 0 || r.MesosDelta > maxMesos || r.MesosDelta < -maxMesos {
		return domain.ScriptExchangeResult{}, domain.ErrInvalidInput
	}
	if r.EventCredit != 0 && r.EventCredit != 250 && r.EventCredit != 500 && r.EventCredit != 750 {
		return domain.ScriptExchangeResult{}, domain.ErrInvalidInput
	}
	if err := validateScriptTravel(r); err != nil {
		return domain.ScriptExchangeResult{}, err
	}
	if r.Look != nil && !domain.ValidScriptLook(*r.Look) {
		return domain.ScriptExchangeResult{}, domain.ErrInvalidInput
	}
	if r.Growth != nil && !domain.ValidScriptGrowth(*r.Growth) {
		return domain.ScriptExchangeResult{}, domain.ErrInvalidInput
	}
	if r.PetTraining && (r.Session.Character.MapID != 100000202 || len(r.Items) != 1 || r.Items[0] != (domain.ScriptItemDelta{TemplateID: 4031035, Quantity: -1})) {
		return domain.ScriptExchangeResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ScriptExchangeResult{}, err
	}
	defer tx.Rollback()
	result, err := executeScriptExchangeTx(ctx, tx, r)
	if err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	if !result.Replayed {
		s.roster.Invalidate(ctx, result.Character.AccountID, result.Character.WorldID)
	}
	return result, nil
}

func executeScriptExchangeTx(ctx context.Context, tx *sql.Tx, r domain.ScriptExchange) (domain.ScriptExchangeResult, error) {
	value, err := lockInventoryCharacter(ctx, tx, r.Session)
	if err != nil {
		return domain.ScriptExchangeResult{}, err
	}
	var hash string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT request_hash,result FROM asset_script_commands WHERE character_id=$1 AND operation_id=$2`, value.ID, r.OperationID).Scan(&hash, &raw)
	if err == nil {
		if hash != scriptExchangeHash(r) {
			return domain.ScriptExchangeResult{}, domain.ErrForbidden
		}
		var prior domain.ScriptExchangeResult
		if err = json.Unmarshal(raw, &prior); err != nil {
			return prior, err
		}
		prior.Character = value
		prior.Replayed = true
		prior.Changes = nil
		prior.Pets = nil
		return prior, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.ScriptExchangeResult{}, err
	}
	if value.Revision != r.Session.Character.Revision {
		return domain.ScriptExchangeResult{}, domain.ErrStaleOwner
	}
	if r.Travel != nil && value.MapID != r.Travel.FromMap {
		return domain.ScriptExchangeResult{}, domain.ErrStaleOwner
	}
	value.HP, value.MP = r.Session.Character.HP, r.Session.Character.MP
	if value.HP < 0 || value.HP > value.MaxHP || value.MP < 0 || value.MP > value.MaxMP {
		return domain.ScriptExchangeResult{}, domain.ErrInvalidInput
	}
	if err = validateTaxiExchange(r, value); err != nil {
		return domain.ScriptExchangeResult{}, err
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return domain.ScriptExchangeResult{}, err
	}
	type entry struct {
		k storedLogKey
		c domain.ScriptLogChange
	}
	entries := make([]entry, 0, len(r.Logs))
	seen := map[string]bool{}
	for _, change := range r.Logs {
		k, e := logKey(r.Session, change.Key, now)
		if e != nil {
			return domain.ScriptExchangeResult{}, e
		}
		if seen[k.order()] || change.Set != nil && change.Delta != 0 {
			return domain.ScriptExchangeResult{}, domain.ErrInvalidInput
		}
		seen[k.order()] = true
		entries = append(entries, entry{k, change})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].k.order() < entries[j].k.order() })
	result := domain.ScriptExchangeResult{InventoryResult: domain.InventoryResult{Character: value}, Logs: make([]domain.ScriptLogValue, 0, len(entries))}
	for _, entry := range entries {
		k, change := entry.k, entry.c
		if _, err = tx.ExecContext(ctx, `INSERT INTO game_script_logs(scope,owner_id,world_id,name,period,window_start,value) VALUES($1,$2,$3,$4,$5,$6,0) ON CONFLICT DO NOTHING`, k.args()...); err != nil {
			return result, err
		}
		log, err := readLog(ctx, tx, k, true)
		if err != nil {
			return result, err
		}
		if change.Expected != nil && log.Value != *change.Expected {
			return result, domain.ErrAlreadyExists
		}
		if change.Delta > 0 && log.Value > math.MaxInt64-change.Delta || change.Delta < 0 && log.Value < math.MinInt64-change.Delta {
			return result, domain.ErrInvalidInput
		}
		next := log.Value + change.Delta
		if change.Set != nil {
			next = *change.Set
		}
		if change.Maximum != nil && next > *change.Maximum {
			return result, domain.ErrAlreadyExists
		}
		args := append(k.args(), next)
		if err = tx.QueryRowContext(ctx, `UPDATE game_script_logs SET value=$7,revision=revision+1,updated_at=clock_timestamp() WHERE scope=$1 AND owner_id=$2 AND world_id=$3 AND name=$4 AND period=$5 AND window_start=$6 RETURNING revision`, args...).Scan(&log.Revision); err != nil {
			return result, err
		}
		log.Value = next
		result.Logs = append(result.Logs, log)
	}
	result.Character.Mesos += r.MesosDelta
	if result.Character.Mesos < 0 {
		return result, domain.ErrInsufficient
	}
	if result.Character.Mesos > domain.MaxMoney() {
		return result, domain.ErrInvalidInput
	}
	if result.QuestData, err = applyScriptQuestData(ctx, tx, value.ID, r.QuestData); err != nil {
		return result, err
	}
	setQuestRecords(&result.Character, r.QuestData)
	for _, delta := range r.Items {
		// Grants share the quest reward allocator so equipment rewards work;
		// removals stay limited to stackable, non-cash items.
		if delta.Quantity > 0 {
			err = applyQuestItem(ctx, tx, &result.InventoryResult, content.QuestItem{ID: delta.TemplateID, Count: delta.Quantity})
		} else {
			err = applyScriptItem(ctx, tx, &result.InventoryResult, delta)
		}
		if err != nil {
			return result, err
		}
	}
	if r.PetTraining {
		if value.MapID != 100000202 {
			return result, domain.ErrStaleOwner
		}
		result.Pets, err = trainSummonedPets(ctx, tx, &result.Character, now)
		if err != nil {
			return result, err
		}
	}
	if r.Look != nil {
		if err = applyScriptLook(ctx, tx, &result.InventoryResult, *r.Look); err != nil {
			return result, err
		}
	}
	if r.Profession != nil {
		if err = applyProfessionChange(ctx, tx, &result.Character, *r.Profession); err != nil {
			return result, err
		}
	}
	if result.Drops, err = applyScriptDrops(ctx, tx, r.Session, result.Character, r.OperationID, r.Drops); err != nil {
		return result, err
	}
	if r.Growth != nil {
		if r.Growth.Coupon != 0 {
			// Uses one coupon; the look itself is set just below.
			if err = applyScriptLook(ctx, tx, &result.InventoryResult, domain.ScriptLook{Coupon: r.Growth.Coupon, Hair: result.Character.Hair}); err != nil {
				return result, err
			}
		}
		if result.Skills, err = applyScriptGrowth(ctx, tx, &result.Character, *r.Growth); err != nil {
			return result, err
		}
	}
	if r.EXP > 0 {
		if err = applyQuestExperience(&result.Character, r.EXP, r.OperationID); err != nil {
			return result, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE game_characters SET level=$1,exp=$2,str=$3,ap=$4,sp=$5 WHERE id=$6`, result.Character.Level, result.Character.EXP, result.Character.STR, result.Character.AP, result.Character.SP, value.ID); err != nil {
			return result, err
		}
	}
	if r.EventCredit > 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO asset_cash_wallets(account_id,credit) VALUES($1,$2)
			ON CONFLICT (account_id) DO UPDATE SET credit=asset_cash_wallets.credit+EXCLUDED.credit`, value.AccountID, r.EventCredit); err != nil {
			return result, err
		}
	}
	if result.Quests, err = applyScriptQuestForces(ctx, tx, &result.Character, r.Quests, now); err != nil {
		return result, err
	}
	if t := r.Travel; t != nil {
		result.Character.MapID, result.Character.SpawnPoint, result.Character.X, result.Character.Y = t.MapID, t.SpawnPoint, t.X, t.Y
		if _, err = tx.ExecContext(ctx, `UPDATE game_characters SET map_id=$1,spawn_point=$2,x=$3,y=$4 WHERE id=$5`, t.MapID, t.SpawnPoint, t.X, t.Y, value.ID); err != nil {
			return result, err
		}
	}
	if err = commitInventoryState(ctx, tx, &result.Character); err != nil {
		return result, err
	}
	raw, err = json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_script_commands(character_id,operation_id,request_hash,result) VALUES($1,$2,$3,$4)`, value.ID, r.OperationID, scriptExchangeHash(r), raw); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Store) ExecuteEventItems(ctx context.Context, exchanges []domain.ScriptExchange) ([]domain.ScriptExchangeResult, error) {
	requests := make([]domain.ScriptExchange, len(exchanges))
	for i, exchange := range exchanges {
		if len(exchange.Items) != 1 {
			return nil, domain.ErrInvalidInput
		}
		requests[i] = domain.ScriptExchange{Session: exchange.Session, OperationID: exchange.OperationID, Items: exchange.Items}
	}
	return s.executeEventExchanges(ctx, requests)
}

func (s *Store) ExecuteEventLogs(ctx context.Context, exchanges []domain.ScriptExchange) ([]domain.ScriptExchangeResult, error) {
	requests := make([]domain.ScriptExchange, len(exchanges))
	for i, exchange := range exchanges {
		if len(exchange.Logs) != 1 {
			return nil, domain.ErrInvalidInput
		}
		requests[i] = domain.ScriptExchange{Session: exchange.Session, OperationID: exchange.OperationID, Logs: exchange.Logs}
	}
	return s.executeEventExchanges(ctx, requests)
}

func (s *Store) executeEventExchanges(ctx context.Context, exchanges []domain.ScriptExchange) ([]domain.ScriptExchangeResult, error) {
	if len(exchanges) < 1 || len(exchanges) > 6 {
		return nil, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	results := make([]domain.ScriptExchangeResult, 0, len(exchanges))
	var lastID int64
	for _, exchange := range exchanges {
		id := exchange.Session.CharacterID
		if id <= lastID || len(exchange.OperationID) < 16 || len(exchange.OperationID) > 128 {
			return nil, domain.ErrInvalidInput
		}
		lastID = id
		result, err := executeScriptExchangeTx(ctx, tx, exchange)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for _, result := range results {
		if !result.Replayed {
			s.roster.Invalidate(ctx, result.Character.AccountID, result.Character.WorldID)
		}
	}
	return results, nil
}

// applyScriptGrowth is the character part of a generated Legacy NPC call.
// A job is only the next advancement of the branch the character is already
// in (x0 to x1 from level 70, x1 to x2 from level 120), with Legacy's skill
// points for it; anything else is refused.
func applyScriptGrowth(ctx context.Context, tx *sql.Tx, c *domain.Character, g domain.ScriptGrowth) ([]domain.SkillState, error) {
	c.Fame += g.Fame
	if c.Fame < -30000 || c.Fame > 30000 {
		return nil, domain.ErrInsufficient
	}
	if g.Job != 0 && g.Job != c.Job {
		tier := g.Job % 10
		if g.Job != c.Job+1 || c.Job%100 == 0 || tier != 1 && tier != 2 || tier == 1 && c.Level < 70 || tier == 2 && c.Level < 120 {
			return nil, domain.ErrForbidden
		}
		c.SetJob(g.Job)
		c.SubJob = domain.SubJobFor(g.Job)
		c.SP++
		if tier == 2 {
			c.SP += 2
		}
	}
	if g.SPSet != nil {
		c.SP = *g.SPSet
	}
	c.SP += g.SP
	if c.SP < 0 {
		return nil, domain.ErrInsufficient
	}
	c.SP = min(c.SP, 999)
	if g.Hair != 0 {
		c.Hair = g.Hair
	}
	if g.Face != 0 {
		c.Face = g.Face
	}
	if g.Skin != nil {
		c.Skin = *g.Skin
	}
	if _, err := tx.ExecContext(ctx, `UPDATE game_characters SET fame=$1,job=$2,sp=$3,hair=$4,face=$5,skin=$6 WHERE id=$7`, c.Fame, c.Job, c.SP, c.Hair, c.Face, c.Skin, c.ID); err != nil {
		return nil, err
	}
	if err := saveJobState(ctx, tx, *c); err != nil {
		return nil, err
	}
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	var taught []domain.SkillState
	for _, s := range g.Skills {
		template, err := content.SkillDefinition(root, s.ID)
		if err != nil || template.Disabled || s.Level > template.MaxLevel || s.Level == 0 {
			// Legacy ignores a skill the game data does not have.
			continue
		}
		if err = grantJobSkills(ctx, tx, c, []domain.SkillState{{ID: s.ID, Level: s.Level, MasterLevel: min(s.Master, template.MaxLevel)}}); err != nil {
			return nil, err
		}
		for _, owned := range c.Skills {
			if owned.ID == s.ID {
				taught = append(taught, owned)
			}
		}
	}
	return taught, nil
}

// applyScriptLook uses one coupon and sets the new hair or face. Coupons are
// cash items, which applyScriptItem refuses; syncCashLocations then retires
// or shrinks the cash serial along with the bag stack.
func applyScriptLook(ctx context.Context, tx *sql.Tx, result *domain.InventoryResult, look domain.ScriptLook) error {
	c := &result.Character
	for i := range c.Items {
		item := &c.Items[i]
		if item.TemplateID != look.Coupon || item.Quantity < 1 {
			continue
		}
		if err := guardItemExpiry(ctx, tx, item.ExpiresAt); errors.Is(err, domain.ErrForbidden) {
			continue // an expired coupon cannot be used
		} else if err != nil {
			return err
		}
		item.Quantity--
		item.Revision++
		if item.Quantity == 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM asset_items WHERE id=$1`, item.ID); err != nil {
				return err
			}
			result.Changes = append(result.Changes, domain.InventoryChange{Mode: 3, InvType: item.InvType, Slot: item.Slot})
			c.Items = append(c.Items[:i], c.Items[i+1:]...)
		} else {
			if _, err := tx.ExecContext(ctx, `UPDATE asset_items SET quantity=$1,revision=revision+1 WHERE id=$2`, item.Quantity, item.ID); err != nil {
				return err
			}
			result.Changes = append(result.Changes, domain.InventoryChange{Mode: 1, InvType: item.InvType, Slot: item.Slot, Quantity: item.Quantity})
		}
		if look.Hair != 0 {
			c.Hair = look.Hair
		} else {
			c.Face = look.Face
		}
		_, err := tx.ExecContext(ctx, `UPDATE game_characters SET hair=$1,face=$2 WHERE id=$3`, c.Hair, c.Face, c.ID)
		return err
	}
	return domain.ErrInsufficient
}

func applyScriptItem(ctx context.Context, tx *sql.Tx, result *domain.InventoryResult, delta domain.ScriptItemDelta) error {
	def, err := inventoryDefinition(delta.TemplateID)
	if err != nil {
		return domain.ErrInvalidInput
	}
	if def.InvType < 2 || def.InvType > 4 || def.Cash || delta.Quantity == 0 || delta.Quantity < -30000 || delta.Quantity > 30000 {
		return domain.ErrInvalidInput
	}
	c := &result.Character
	remaining := delta.Quantity
	if remaining < 0 {
		for i := 0; i < len(c.Items) && remaining < 0; i++ {
			item := &c.Items[i]
			if item.TemplateID != delta.TemplateID {
				continue
			}
			take := min(item.Quantity, -remaining)
			item.Quantity -= take
			remaining += take
			item.Revision++
			if item.Quantity == 0 {
				if _, err = tx.ExecContext(ctx, `DELETE FROM asset_items WHERE id=$1`, item.ID); err != nil {
					return err
				}
				result.Changes = append(result.Changes, domain.InventoryChange{Mode: 3, InvType: item.InvType, Slot: item.Slot})
				c.Items = append(c.Items[:i], c.Items[i+1:]...)
				i--
			} else {
				if _, err = tx.ExecContext(ctx, `UPDATE asset_items SET quantity=$1,revision=revision+1 WHERE id=$2`, item.Quantity, item.ID); err != nil {
					return err
				}
				result.Changes = append(result.Changes, domain.InventoryChange{Mode: 1, InvType: item.InvType, Slot: item.Slot, Quantity: item.Quantity})
			}
		}
		if remaining < 0 {
			return domain.ErrInsufficient
		}
		return nil
	}
	for i := range c.Items {
		item := &c.Items[i]
		if item.TemplateID != delta.TemplateID || remaining == 0 {
			continue
		}
		add := min(remaining, def.SlotMax-item.Quantity)
		if add <= 0 {
			continue
		}
		item.Quantity += add
		item.Revision++
		remaining -= add
		if _, err = tx.ExecContext(ctx, `UPDATE asset_items SET quantity=$1,revision=revision+1 WHERE id=$2`, item.Quantity, item.ID); err != nil {
			return err
		}
		result.Changes = append(result.Changes, domain.InventoryChange{Mode: 1, InvType: item.InvType, Slot: item.Slot, Quantity: item.Quantity})
	}
	for remaining > 0 {
		used := map[int]bool{}
		for _, item := range c.Items {
			if item.InvType == def.InvType {
				used[item.Slot] = true
			}
		}
		slot := 1
		for used[slot] && slot <= c.Capacity(def.InvType) {
			slot++
		}
		if slot > c.Capacity(def.InvType) {
			return domain.ErrInsufficient
		}
		item := domain.InventoryItem{TemplateID: delta.TemplateID, InvType: def.InvType, Slot: slot, Quantity: min(remaining, def.SlotMax), Revision: 1}
		if err = tx.QueryRowContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,$2,$3,$4,$5) RETURNING id`, c.ID, item.InvType, item.Slot, item.TemplateID, item.Quantity).Scan(&item.ID); err != nil {
			return err
		}
		remaining -= item.Quantity
		c.Items = append(c.Items, item)
		copyItem := item
		result.Changes = append(result.Changes, domain.InventoryChange{Mode: 0, InvType: item.InvType, Slot: item.Slot, Item: &copyItem})
	}
	return nil
}
