package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

// professionFatigueSeconds is how long one point of fatigue takes to wear off.
const professionFatigueSeconds = content.ProfessionFatigueSeconds

// readProfessionState loads professions, learned recipes, traits and the
// fatigue left after the time since it was last written.
func readProfessionState(ctx context.Context, q questQuerier, c *domain.Character) error {
	rows, err := q.QueryContext(ctx, `SELECT skill_id,level,exp FROM game_professions WHERE character_id=$1 ORDER BY skill_id`, c.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var p domain.ProfessionState
		if err = rows.Scan(&p.ID, &p.Level, &p.EXP); err != nil {
			rows.Close()
			return err
		}
		c.Professions = append(c.Professions, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = q.QueryContext(ctx, `SELECT recipe_id FROM game_profession_recipes WHERE character_id=$1 ORDER BY recipe_id`, c.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		c.Recipes = append(c.Recipes, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = q.QueryContext(ctx, `SELECT charisma,insight,will,craft,sense,charm,fatigue,GREATEST(0,EXTRACT(EPOCH FROM clock_timestamp()-fatigue_at))::bigint FROM game_character_traits WHERE character_id=$1`, c.ID)
	if err != nil {
		return err
	}
	if rows.Next() {
		var fatigue int
		var elapsed int64
		if err = rows.Scan(&c.Traits.Charisma, &c.Traits.Insight, &c.Traits.Will, &c.Traits.Craft, &c.Traits.Sense, &c.Traits.Charm, &fatigue, &elapsed); err != nil {
			rows.Close()
			return err
		}
		c.Fatigue = int(max(0, int64(fatigue)-elapsed/professionFatigueSeconds))
	}
	err = rows.Err()
	rows.Close()
	return err
}

func validProfessionChange(p domain.ProfessionChange) bool {
	t := p.Traits
	for _, v := range []int{t.Charisma, t.Insight, t.Will, t.Craft, t.Sense, t.Charm} {
		if v < 0 || v > 1000 {
			return false
		}
	}
	if p.Learn != 0 && !content.ValidProfession(p.Learn) || p.Forget != 0 && !content.ValidProfession(p.Forget) || p.Learn != 0 && p.Learn == p.Forget {
		return false
	}
	if p.EXP < 0 || p.EXP > 10000 || (p.EXP != 0) != (p.Gain != 0) || p.Gain != 0 && !content.ValidProfession(p.Gain) {
		return false
	}
	if p.Recipe != 0 && (p.Recipe%10000 == 0 || !content.ValidProfession(p.Recipe/10000*10000)) {
		return false
	}
	return p.Fatigue >= 0 && p.Fatigue <= content.ProfessionFatigueLimit
}

// applyProfessionChange commits one profession change inside the exchange
// transaction. A rule the character does not meet is ErrInsufficient, which
// the script host reports as an ordinary refusal.
func applyProfessionChange(ctx context.Context, tx *sql.Tx, c *domain.Character, p domain.ProfessionChange) error {
	if !validProfessionChange(p) {
		return domain.ErrInvalidInput
	}
	if id := p.Forget; id != 0 {
		if c.Profession(id).Level == 0 {
			return domain.ErrInsufficient
		}
		for _, other := range c.Professions {
			// A craft keeps its gathering profession in place.
			if content.ProfessionRequires(other.ID) == id {
				return domain.ErrInsufficient
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM game_professions WHERE character_id=$1 AND skill_id=$2`, c.ID, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM game_profession_recipes WHERE character_id=$1 AND recipe_id/10000=$2`, c.ID, id/10000); err != nil {
			return err
		}
		var kept []domain.ProfessionState
		for _, other := range c.Professions {
			if other.ID != id {
				kept = append(kept, other)
			}
		}
		c.Professions = kept
		var recipes []int
		for _, r := range c.Recipes {
			if r/10000 != id/10000 {
				recipes = append(recipes, r)
			}
		}
		c.Recipes = recipes
	}
	if id := p.Learn; id != 0 {
		if c.Profession(id).Level != 0 {
			return domain.ErrInsufficient
		}
		need := content.ProfessionRequires(id)
		if need != 0 && c.Profession(need).Level == 0 {
			return domain.ErrInsufficient
		}
		for _, other := range c.Professions {
			// One gathering profession and one craft at a time.
			if content.ProfessionCraft(other.ID) == content.ProfessionCraft(id) {
				return domain.ErrInsufficient
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_professions(character_id,skill_id,level,exp) VALUES($1,$2,1,0)`, c.ID, id); err != nil {
			return err
		}
		c.Professions = append(append([]domain.ProfessionState(nil), c.Professions...), domain.ProfessionState{ID: id, Level: 1})
	}
	if id := p.Gain; id != 0 {
		state := c.Profession(id)
		if state.Level == 0 {
			return domain.ErrInsufficient
		}
		root := os.Getenv("MAPLEGO_WZ_ROOT")
		state.EXP += p.EXP
		for {
			need := content.ProfessionExpToNext(root, id, state.Level)
			if need == 0 {
				state.EXP = 0 // the last level gathers no more proficiency
				break
			}
			if state.EXP < need {
				break
			}
			state.EXP -= need
			state.Level++
		}
		if _, err := tx.ExecContext(ctx, `UPDATE game_professions SET level=$1,exp=$2 WHERE character_id=$3 AND skill_id=$4`, state.Level, state.EXP, c.ID, id); err != nil {
			return err
		}
		next := append([]domain.ProfessionState(nil), c.Professions...)
		for i := range next {
			if next[i].ID == id {
				next[i] = state
			}
		}
		c.Professions = next
	}
	if id := p.Recipe; id != 0 {
		if c.KnowsRecipe(id) || c.Profession(id/10000*10000).Level == 0 {
			return domain.ErrInsufficient
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_profession_recipes(character_id,recipe_id) VALUES($1,$2)`, c.ID, id); err != nil {
			return err
		}
		c.Recipes = append(append([]int(nil), c.Recipes...), id)
	}
	if p.Traits != (domain.Traits{}) {
		t := c.Traits.Add(p.Traits)
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_character_traits(character_id,charisma,insight,will,craft,sense,charm) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(character_id) DO UPDATE SET charisma=$2,insight=$3,will=$4,craft=$5,sense=$6,charm=$7`, c.ID, t.Charisma, t.Insight, t.Will, t.Craft, t.Sense, t.Charm); err != nil {
			return err
		}
		c.Traits = t
	}
	if p.Fatigue != 0 {
		fatigue := c.Fatigue + p.Fatigue
		if fatigue > content.ProfessionFatigueLimit {
			return domain.ErrInsufficient
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_character_traits(character_id,fatigue,fatigue_at) VALUES($1,$2,clock_timestamp()) ON CONFLICT(character_id) DO UPDATE SET fatigue=$2,fatigue_at=clock_timestamp()`, c.ID, fatigue); err != nil {
			return err
		}
		c.Fatigue = fatigue
	}
	return nil
}

// applyScriptDrops leaves the exchange's drops on the character's map. They
// are ordinary item drops: the owner picks them up, and they expire, like
// monster loot. A full map refuses the whole exchange.
func applyScriptDrops(ctx context.Context, tx *sql.Tx, session domain.CharacterSession, c domain.Character, operation string, drops []domain.ScriptDrop) ([]domain.ItemDrop, error) {
	if len(drops) == 0 {
		return nil, nil
	}
	world, channel := session.Lease.WorldID, session.ChannelID
	if channel < 1 || channel > 20 {
		return nil, domain.ErrInvalidInput
	}
	room := fmt.Sprintf("item-room/%d/%d/%d", world, channel, c.MapID)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, room); err != nil {
		return nil, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM asset_item_drops WHERE world_id=$1 AND channel_id=$2 AND map_id=$3 AND picked_up_by IS NULL AND expired_at IS NULL AND expires_at>clock_timestamp()`, world, channel, c.MapID).Scan(&count); err != nil {
		return nil, err
	}
	if count+len(drops) > 512 {
		return nil, domain.ErrInsufficient
	}
	out := make([]domain.ItemDrop, 0, len(drops))
	for _, d := range drops {
		def, err := inventoryDefinition(d.TemplateID)
		castlePrize := content.JudgementRoom(c.MapID) && session.Character.MapID == c.MapID
		if err != nil || def.InvType < 1 || def.InvType > 4 || (def.Cash && !(castlePrize && def.InvType == 1 && d.TemplateID/10000 == 170)) ||
			(d.RollEquipment && !(castlePrize && def.InvType == 1)) || d.Quantity < 1 || d.Quantity > def.SlotMax || d.X < -32768 || d.X > 32767 || d.Y < -32768 || d.Y > 32767 {
			return nil, domain.ErrInvalidInput
		}
		drop := domain.ItemDrop{WorldID: world, ChannelID: channel, MapID: c.MapID, OwnerID: c.ID, X: d.X, Y: d.Y, InvType: def.InvType, TemplateID: d.TemplateID, Quantity: d.Quantity}
		var equipment []byte
		if castlePrize && def.InvType == 1 {
			gear := def.Equipment
			if d.RollEquipment {
				gear = rolledLootEquipment(gear, operation, d.TemplateID)
			}
			drop.Equipment = &gear
			equipment, err = json.Marshal(gear)
			if err != nil {
				return nil, err
			}
		}
		if err = tx.QueryRowContext(ctx, `INSERT INTO asset_item_drops(world_id,channel_id,map_id,owner_id,x,y,inv_type,template_id,quantity,equipment) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING object_id`, drop.WorldID, drop.ChannelID, drop.MapID, drop.OwnerID, drop.X, drop.Y, drop.InvType, drop.TemplateID, drop.Quantity, nullableJSON(equipment)).Scan(&drop.ObjectID); err != nil {
			return nil, err
		}
		out = append(out, drop)
	}
	return out, nil
}
