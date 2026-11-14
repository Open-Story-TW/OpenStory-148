package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"

	"maple-go-server/internal/domain"
)

type equipmentMetadata struct {
	ScrollLevel int                       `json:"scroll_level,omitempty"`
	Flags       int                       `json:"flags,omitempty"`
	Potential   domain.EquipmentPotential `json:"potential,omitzero"`
	Enhance     uint8                     `json:"enhance,omitempty"`
}

// Unknown metadata must block the command, not disappear on a later narrow
// remarshal. This is deliberately strict rather than retaining mutable maps in
// detached equipment values or attempting to interpret a future schema.
func (m *equipmentMetadata) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("equipment metadata must be an object")
	}
	type value equipmentMetadata
	var next value
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&next); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("equipment metadata requires one JSON object")
	}
	*m = equipmentMetadata(next)
	return nil
}

func applyEquipmentMetadata(ctx context.Context, q txQuery, c *domain.Character) error {
	rows, err := q.QueryContext(ctx, `SELECT m.equipment_id,m.metadata FROM asset_equipment_metadata m JOIN asset_equipment e ON e.id=m.equipment_id WHERE e.character_id=$1 AND m.account_id=$2`, c.ID, c.AccountID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			return err
		}
		var m equipmentMetadata
		if err = json.Unmarshal(raw, &m); err != nil {
			return err
		}
		if m.ScrollLevel < 0 || m.ScrollLevel > 255 || m.Flags < 0 || m.Flags > 65535 {
			return domain.ErrInvalidInput
		}
		for i := range c.Equipment {
			if c.Equipment[i].ID == id {
				c.Equipment[i].ScrollLevel = m.ScrollLevel
				c.Equipment[i].Flags = m.Flags
				c.Equipment[i].Potential = m.Potential
				c.Equipment[i].Enhance = m.Enhance
				break
			}
		}
	}
	return rows.Err()
}

func persistEquipmentMetadata(ctx context.Context, tx *sql.Tx, c domain.Character) error {
	for _, e := range c.Equipment {
		if e.ScrollLevel < 0 || e.ScrollLevel > 255 || e.Flags < 0 || e.Flags > 65535 {
			return domain.ErrInvalidInput
		}
		if e.ScrollLevel == 0 && e.Flags == 0 && e.Potential.IsZero() && e.Enhance == 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM asset_equipment_metadata WHERE equipment_id=$1`, e.ID); err != nil {
				return err
			}
			continue
		}
		raw, err := json.Marshal(equipmentMetadata{ScrollLevel: e.ScrollLevel, Flags: e.Flags, Potential: e.Potential, Enhance: e.Enhance})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO asset_equipment_metadata(equipment_id,account_id,metadata) VALUES($1,$2,$3) ON CONFLICT(equipment_id) DO UPDATE SET account_id=EXCLUDED.account_id,metadata=EXCLUDED.metadata`, e.ID, c.AccountID, raw); err != nil {
			return err
		}
	}
	return nil
}
