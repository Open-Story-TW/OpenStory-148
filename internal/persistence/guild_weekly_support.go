package persistence

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

// applyGuildWeeklySupport runs inside the quest exchange's inventory and
// weekly-log transaction. Lock membership/rank so an expulsion or rank change
// cannot make an already committed reward use an invalid source.
func applyGuildWeeklySupport(ctx context.Context, tx *sql.Tx, result *domain.InventoryResult, now time.Time) error {
	if now.In(scriptLogZone).Weekday() != time.Monday {
		return domain.ErrForbidden
	}
	c := &result.Character
	var rank int
	err := tx.QueryRowContext(ctx, `SELECT k.level FROM game_guild_members m JOIN game_guild_skills k ON k.guild_id=m.guild_id
	WHERE m.character_id=$1 AND k.skill_id=$2 AND k.expires_at=-1 FOR SHARE OF m,k`, c.ID, domain.GuildSupportSkillID).Scan(&rank)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrForbidden
	}
	if err != nil {
		return err
	}
	def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), domain.GuildSupportSkillID)
	if err != nil || def.Disabled {
		return domain.ErrInvalidInput
	}
	values, err := def.Values(rank)
	if err != nil {
		return domain.ErrInvalidInput
	}
	count := values["u"]
	potion, err := inventoryDefinition(domain.GuildSupportPotion)
	if err != nil || potion.InvType != 2 || potion.Cash || !potion.TradeBlock || count < 1 || count > potion.SlotMax {
		return domain.ErrInvalidInput
	}
	used := make(map[int]bool)
	for _, item := range c.Items {
		if item.InvType == potion.InvType {
			used[item.Slot] = true
		}
	}
	slot := 1
	for used[slot] {
		slot++
	}
	if slot > c.Capacity(potion.InvType) {
		return domain.ErrInsufficient
	}
	item := domain.InventoryItem{TemplateID: domain.GuildSupportPotion, InvType: potion.InvType,
		Slot: slot, Quantity: count, Revision: 1, ExpiresAt: now.AddDate(0, 0, 7).UnixMilli()}
	if err = tx.QueryRowContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity,expires_at)
	VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, c.ID, item.InvType, item.Slot, item.TemplateID, item.Quantity, item.ExpiresAt).Scan(&item.ID); err != nil {
		return err
	}
	c.Items = append(c.Items, item)
	result.Changes = append(result.Changes, domain.InventoryChange{Mode: 0, InvType: item.InvType, Slot: item.Slot, Item: &item})
	return nil
}
