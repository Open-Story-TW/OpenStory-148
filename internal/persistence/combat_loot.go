package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

var _ domain.CombatLootRepository = (*Store)(nil)

type plannedCombatLoot struct {
	Items []domain.ItemDrop `json:"items"`
	// Currency and unsupported-item policies remain visible for the coverage
	// audit; they are never silently turned into a different item or meso bag.
	Unsupported []int `json:"unsupported_source_rows,omitempty"`
}

func lootRoll(operationID string, rowID, part int, n uint64) uint64 {
	hash := sha256.Sum256([]byte(operationID + "/" + strconv.Itoa(rowID) + "/" + strconv.Itoa(part)))
	return binary.LittleEndian.Uint64(hash[:8]) % n
}

// Stats for monster equipment vary by up to 10%, capped per attribute.
// The kill and source row also seed the rolls so the sealed plan can replay.
func rolledLootEquipment(base domain.Equipment, operationID string, rowID int) domain.Equipment {
	for i, stat := range []struct {
		value *int
		limit int
	}{
		{&base.STR, 5}, {&base.DEX, 5}, {&base.INT, 5}, {&base.LUK, 5},
		{&base.MATK, 5}, {&base.WATK, 5}, {&base.ACC, 5}, {&base.AVOID, 5},
		{&base.Jump, 5}, {&base.Speed, 5},
		{&base.WDEF, 10}, {&base.MDEF, 10}, {&base.HP, 10}, {&base.MP, 10},
	} {
		if *stat.value > 0 {
			radius := min((*stat.value+9)/10, stat.limit)
			*stat.value += int(lootRoll(operationID, rowID, i+2, uint64(radius*2+1))) - radius
		}
	}
	return base
}

// enqueueCombatLootTx must follow the asset_combat_rewards insert in the SAME
// transaction. It pins random outcomes and equipment snapshots before commit.
func enqueueCombatLootTx(ctx context.Context, tx *sql.Tx, session domain.CharacterSession, value domain.Character, operationID string, mobID, x, y int) error {
	if content.JudgementRoom(value.MapID) || content.SubwaySurvivalStage(value.MapID) {
		return nil
	}
	if session.ChannelID < 1 || session.ChannelID > 40 || x < -32768 || x > 32767 || y < -32768 || y > 32767 {
		return domain.ErrInvalidInput
	}
	rows, err := content.MonsterDrops(mobID, value.MapID)
	if err != nil {
		return err
	}
	mesoRule, err := content.MonsterMesoRule(os.Getenv("MAPLEGO_WZ_ROOT"), mobID)
	if err != nil {
		return err
	}
	hasMesoRow := false
	for _, row := range rows {
		hasMesoRow = hasMesoRow || !row.Global && row.TemplateID == 0
	}
	if !hasMesoRow {
		for i := 0; i < mesoRule.Count; i++ {
			rows = append(rows, content.DropDefinition{ID: -1 - i, MobID: mobID, Minimum: mesoRule.Minimum, Maximum: mesoRule.Maximum, Chance: mesoRule.Chance})
		}
	}
	active := make(map[int]bool)
	for _, q := range value.Quests {
		active[q.ID] = q.State == 1
	}
	plan := plannedCombatLoot{}
	plannedQuestItems := map[[2]int]int{}
	var questPartyID int64
	questDropType := -1
	now := time.Now()
	mesoBonus := skills.Bonuses(value, now).MesoRate
	if buffBonus, _ := skills.BuffBonus(value, now); buffBonus.MesoRate > 0 {
		mesoBonus += buffBonus.MesoRate
	}
	mesoDropped := false
	for _, row := range rows {
		if row.Chance <= 0 || row.QuestID > 0 && !active[row.QuestID] {
			continue
		}
		if row.TemplateID == 0 {
			if row.QuestID > 0 {
				plan.Unsupported = append(plan.Unsupported, row.ID)
				continue
			}
			if row.Global {
				plan.Unsupported = append(plan.Unsupported, row.ID)
				continue // A global zero-item row is NX, never ground mesos.
			}
			if mesoDropped && !mesoRule.Explosive || lootRoll(operationID, row.ID, 0, 999999) >= uint64(min(row.Chance, 999999)) {
				continue
			}
			amount := int64(row.Minimum) + int64(lootRoll(operationID, row.ID, 1, uint64(row.Maximum-row.Minimum+1)))
			amount = min(amount*domain.MesoRate(), maxMesos)
			if mesoBonus > 0 && amount > 0 {
				amount = min(amount+amount*int64(mesoBonus)/100, maxMesos)
			}
			if amount > 0 && amount <= maxMesos {
				plan.Items = append(plan.Items, domain.ItemDrop{WorldID: value.WorldID, ChannelID: session.ChannelID, MapID: value.MapID,
					OwnerID: value.ID, X: x, Y: y, Quantity: 1, Mesos: amount})
				mesoDropped = true
			}
			continue
		}
		definition, err := inventoryDefinition(row.TemplateID)
		_, amoriaDrop := content.AmoriaMonsterDrop(value.MapID, mobID, row.TemplateID)
		if err != nil || definition.InvType < 1 || definition.InvType > 4 || definition.Cash ||
			definition.Quest && row.QuestID == 0 && !amoriaDrop {
			plan.Unsupported = append(plan.Unsupported, row.ID)
			continue
		}
		chance := min(row.Chance, 999999)
		if definition.InvType == 1 && !row.Global {
			chance = min(chance*10, 999999)
		}
		chance = int(min(int64(chance)*domain.DropRate(), 999999))
		if lootRoll(operationID, row.ID, 0, 999999) >= uint64(chance) {
			continue
		}
		quantity := 1
		if definition.InvType != 1 && row.Maximum != 1 {
			quantity = row.Minimum + int(lootRoll(operationID, row.ID, 1, uint64(max(1, row.Maximum-row.Minimum))))
		}
		if quantity < 1 || quantity > 30000 {
			plan.Unsupported = append(plan.Unsupported, row.ID)
			continue
		}
		drop := domain.ItemDrop{WorldID: value.WorldID, ChannelID: session.ChannelID, MapID: value.MapID, OwnerID: value.ID, X: x, Y: y, InvType: definition.InvType, TemplateID: row.TemplateID, Quantity: quantity}
		if definition.InvType == 1 {
			equipment := rolledLootEquipment(definition.Equipment, operationID, row.ID)
			drop.Equipment = &equipment
		}
		if row.QuestID > 0 {
			remaining, needErr := content.QuestDropRemaining(os.Getenv("MAPLEGO_WZ_ROOT"), value, row.QuestID, row.TemplateID)
			if needErr != nil {
				plan.Unsupported = append(plan.Unsupported, row.ID)
				continue
			}
			key := [2]int{row.QuestID, row.TemplateID}
			remaining -= plannedQuestItems[key]
			if remaining <= 0 {
				continue
			}
			drop.Quantity = min(quantity, remaining)
			if questDropType == -2 {
				plan.Unsupported = append(plan.Unsupported, row.ID)
				continue
			}
			if questDropType < 0 {
				questPartyID, err = partyForCharacter(ctx, tx, value.ID)
				if err != nil {
					return err
				}
				questDropType, err = content.MonsterQuestDropType(os.Getenv("MAPLEGO_WZ_ROOT"), mobID, questPartyID > 0)
				if err != nil {
					questDropType = -2
					plan.Unsupported = append(plan.Unsupported, row.ID)
					continue
				}
			}
			drop.QuestID, drop.QuestDropType, drop.QuestPartyID = row.QuestID, questDropType, questPartyID
			plannedQuestItems[key] += drop.Quantity
		}
		plan.Items = append(plan.Items, drop)
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO asset_combat_loot_jobs(operation_id,owner_id,world_id,channel_id,map_id,mob_id,x,y,loot) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING`, operationID, value.ID, value.WorldID, session.ChannelID, value.MapID, mobID, x, y, raw)
	return err
}

func (s *Store) PendingCombatLoot(ctx context.Context, lease domain.WorldLease, limit int) ([]string, error) {
	if limit < 1 || limit > 64 {
		return nil, domain.ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `SELECT operation_id FROM asset_combat_loot_jobs WHERE world_id=$1 AND materialized_at IS NULL ORDER BY created_at,operation_id LIMIT $2`, lease.WorldID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) MaterializeCombatLoot(ctx context.Context, lease domain.WorldLease, operationID string) ([]domain.ItemDrop, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = guardLease(ctx, tx, lease); err != nil {
		return nil, err
	}
	var world, channel, mapID int
	var owner int64
	var raw []byte
	var done sql.NullTime
	if err = tx.QueryRowContext(ctx, `SELECT world_id,channel_id,map_id,owner_id,loot,materialized_at FROM asset_combat_loot_jobs WHERE operation_id=$1 FOR UPDATE`, operationID).Scan(&world, &channel, &mapID, &owner, &raw, &done); err != nil {
		return nil, err
	}
	if world != lease.WorldID {
		return nil, domain.ErrForbidden
	}
	if done.Valid {
		return nil, nil
	}
	var plan plannedCombatLoot
	if err = json.Unmarshal(raw, &plan); err != nil {
		return nil, err
	}
	room := fmt.Sprintf("item-room/%d/%d/%d", world, channel, mapID)
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, room); err != nil {
		return nil, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM asset_item_drops WHERE world_id=$1 AND channel_id=$2 AND map_id=$3 AND picked_up_by IS NULL AND expired_at IS NULL AND expires_at>clock_timestamp()`, world, channel, mapID).Scan(&count); err != nil {
		return nil, err
	}
	if count+len(plan.Items) > 512 {
		return nil, domain.ErrInsufficient // leave the whole job pending for a retry.
	}
	for i := range plan.Items {
		drop := &plan.Items[i]
		if drop.WorldID != world || drop.ChannelID != channel || drop.MapID != mapID || drop.OwnerID != owner {
			return nil, domain.ErrInvalidInput
		}
		kind := 2
		if drop.QuestID > 0 {
			kind = drop.QuestDropType
		}
		var equipment []byte
		if drop.InvType == 1 {
			if drop.Equipment == nil || drop.Equipment.ItemID != drop.TemplateID || drop.Equipment.ID != 0 || drop.Quantity != 1 {
				return nil, domain.ErrInvalidInput
			}
			if equipment, err = json.Marshal(drop.Equipment); err != nil {
				return nil, err
			}
		}
		if err = tx.QueryRowContext(ctx, `INSERT INTO asset_item_drops(world_id,channel_id,map_id,owner_id,x,y,inv_type,template_id,quantity,mesos,quest_id,quest_drop_type,quest_party_id,quest_owner_until,equipment,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,CASE WHEN $11>0 AND $12<2 THEN clock_timestamp()+interval '30 seconds' ELSE NULL END,$14,clock_timestamp()+make_interval(secs => $15)) RETURNING object_id,COALESCE((extract(epoch FROM quest_owner_until)*1000)::bigint,0)`, drop.WorldID, drop.ChannelID, drop.MapID, drop.OwnerID, drop.X, drop.Y, drop.InvType, drop.TemplateID, drop.Quantity, drop.Mesos, drop.QuestID, kind, drop.QuestPartyID, nullableJSON(equipment), itemDropLifetime(*drop)).Scan(&drop.ObjectID, &drop.QuestOwnerUntil); err != nil {
			return nil, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_combat_loot_jobs SET materialized_at=clock_timestamp() WHERE operation_id=$1`, operationID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return plan.Items, nil
}
