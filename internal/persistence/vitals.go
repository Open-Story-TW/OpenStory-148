package persistence

import (
	"context"
	"database/sql"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"time"
)

func readBaseVitals(ctx context.Context, q questQuerier, c *domain.Character) error {
	rows, err := q.QueryContext(ctx, `SELECT base_max_hp,base_max_mp,vitals_model,vitals_origin FROM game_characters WHERE id=$1`, c.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		return domain.ErrNotFound
	}
	var hp, mp sql.NullInt64
	if err = rows.Scan(&hp, &mp, &c.VitalsModel, &c.VitalsOrigin); err != nil {
		return err
	}
	c.BaseMaxHP, c.BaseMaxMP = int(hp.Int64), int(mp.Int64)
	if err = rows.Err(); err != nil {
		return err
	}
	if c.VitalsModel == 0 {
		return nil
	} // audit-labelled ambiguous aggregate is preserved.
	if err = recomputeVitals(c); err != nil {
		return err
	}
	// A persisted timed passive can expire while offline. Normalize only a
	// successfully derived, known model before the initial character snapshot.
	// This does not heal a dead character or guess an ambiguous legacy base.
	c.HP = min(c.HP, c.MaxHP)
	c.MP = min(c.MP, c.MaxMP)
	return nil
}

func recomputeVitals(c *domain.Character) error {
	hp, mp, err := content.DerivedVitals(*c, time.Now())
	if err != nil {
		return domain.ErrInvalidInput
	}
	c.MaxHP, c.MaxMP = hp, mp
	return nil
}

func recomputeEquipmentVitals(c *domain.Character, oldHP, oldMP int) error {
	if c.VitalsModel == 0 {
		hp, mp := equipmentVitals(c.Equipment)
		if hp != oldHP || mp != oldMP {
			return domain.ErrForbidden
		}
		return nil
	}
	return recomputeVitals(c)
}

func persistBaseVitals(ctx context.Context, tx *sql.Tx, c domain.Character) error {
	if c.VitalsModel == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE game_characters SET base_max_hp=$1,base_max_mp=$2 WHERE id=$3 AND vitals_model=1`, c.BaseMaxHP, c.BaseMaxMP, c.ID)
	return err
}
