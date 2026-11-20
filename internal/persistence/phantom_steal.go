package persistence

import (
	"context"

	"maple-go-server/internal/domain"
)

var _ domain.PhantomStealRepository = (*Store)(nil)

func readPhantomSteal(ctx context.Context, q questQuerier, c *domain.Character) error {
	if !domain.IsPhantomJob(c.Job) {
		return nil
	}
	rows, err := q.QueryContext(ctx, `SELECT tier,slot,skill_id,level,master_level FROM game_phantom_stolen_skills WHERE character_id=$1 ORDER BY tier,slot`, c.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var entry domain.PhantomStolenSkill
		if err = rows.Scan(&entry.Tier, &entry.Slot, &entry.SkillID, &entry.Level, &entry.MasterLevel); err != nil {
			rows.Close()
			return err
		}
		if idx, ok := domain.PhantomSlotOffset(entry.Tier, entry.Slot); ok && entry.SkillID > 0 && entry.Level > 0 {
			c.PhantomSteal.Stolen[idx] = entry
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = q.QueryContext(ctx, `SELECT base_skill_id,chosen_skill_id FROM game_phantom_chosen_skills WHERE character_id=$1 ORDER BY base_skill_id`, c.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var baseID, chosenID int
		if err = rows.Scan(&baseID, &chosenID); err != nil {
			rows.Close()
			return err
		}
		if tier := domain.PhantomBaseTier(baseID); tier >= 1 && tier <= 4 && chosenID > 0 {
			if _, _, ok := c.PhantomSteal.FindStolen(chosenID); ok {
				c.PhantomSteal.Chosen[tier-1] = chosenID
			}
		}
	}
	err = rows.Err()
	rows.Close()
	return err
}

func (s *Store) SavePhantomStealState(ctx context.Context, session domain.CharacterSession, state domain.PhantomStealState) (domain.Character, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Character{}, err
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, session)
	if err != nil {
		return domain.Character{}, err
	}
	if !domain.IsPhantomJob(c.Job) {
		return domain.Character{}, domain.ErrForbidden
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM game_phantom_chosen_skills WHERE character_id=$1`, c.ID); err != nil {
		return domain.Character{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM game_phantom_stolen_skills WHERE character_id=$1`, c.ID); err != nil {
		return domain.Character{}, err
	}
	for _, entry := range state.Stolen {
		if entry.SkillID <= 0 || entry.Level <= 0 {
			continue
		}
		if _, ok := domain.PhantomSlotOffset(entry.Tier, entry.Slot); !ok {
			return domain.Character{}, domain.ErrInvalidInput
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO game_phantom_stolen_skills(character_id,tier,slot,skill_id,level,master_level) VALUES($1,$2,$3,$4,$5,$6)`,
			c.ID, entry.Tier, entry.Slot, entry.SkillID, entry.Level, entry.MasterLevel); err != nil {
			return domain.Character{}, err
		}
	}
	for i, chosen := range state.Chosen {
		if chosen <= 0 {
			continue
		}
		if _, _, ok := state.FindStolen(chosen); !ok {
			return domain.Character{}, domain.ErrInvalidInput
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO game_phantom_chosen_skills(character_id,base_skill_id,chosen_skill_id) VALUES($1,$2,$3)`,
			c.ID, domain.PhantomBaseSkills[i], chosen); err != nil {
			return domain.Character{}, err
		}
	}
	c.PhantomSteal = state
	if err = tx.Commit(); err != nil {
		return domain.Character{}, err
	}
	return c, nil
}
