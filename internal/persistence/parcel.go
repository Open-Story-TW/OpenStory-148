package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"maple-go-server/internal/domain"
)

var _ domain.ParcelRepository = (*Store)(nil)

const parcelColumns = `id,sender_id,sender_name,receiver_id,mesos,inv_type,template_id,quantity,equipment,quick,message,sent_at,arrives_at,expires_at`

func scanParcel(row interface{ Scan(...any) error }) (domain.Parcel, error) {
	var p domain.Parcel
	var invType, templateID, quantity int
	var raw []byte
	if err := row.Scan(&p.ID, &p.SenderID, &p.SenderName, &p.ReceiverID, &p.Mesos, &invType, &templateID, &quantity, &raw, &p.Quick, &p.Message, &p.SentAt, &p.ArrivesAt, &p.ExpiresAt); err != nil {
		return p, err
	}
	switch {
	case len(raw) > 0:
		p.Equipment = new(domain.Equipment)
		if err := json.Unmarshal(raw, p.Equipment); err != nil {
			return p, err
		}
	case invType > 0:
		p.Item = &domain.InventoryItem{InvType: invType, TemplateID: templateID, Quantity: quantity}
	}
	return p, nil
}

func readParcels(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, query string, args ...any) ([]domain.Parcel, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Parcel, 0)
	for rows.Next() {
		p, err := scanParcel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// OpenParcels lists the character's parcel box. Parcels past their thirty
// days are removed here and returned once as Expired, which the client shows
// as "[sender]所傳送的物件已過有效期限！".
func (s *Store) OpenParcels(ctx context.Context, session domain.CharacterSession) (domain.ParcelBox, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ParcelBox{}, err
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, session)
	if err != nil {
		return domain.ParcelBox{}, err
	}
	box := domain.ParcelBox{}
	if box.Expired, err = readParcels(ctx, tx, `DELETE FROM asset_parcels WHERE receiver_id=$1 AND expires_at<=clock_timestamp() RETURNING `+parcelColumns, c.ID); err != nil {
		return box, err
	}
	if box.Parcels, err = readParcels(ctx, tx, `SELECT `+parcelColumns+` FROM asset_parcels WHERE receiver_id=$1 ORDER BY id`, c.ID); err != nil {
		return box, err
	}
	return box, tx.Commit()
}

func (s *Store) HasQuickParcel(ctx context.Context, session domain.CharacterSession) (bool, error) {
	var found bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM asset_parcels WHERE receiver_id=$1 AND quick AND arrives_at<=clock_timestamp() AND expires_at>clock_timestamp())`, session.CharacterID).Scan(&found)
	return found, err
}

// SendParcel takes the mesos, the fee and the item from the sender and puts
// them in the recipient's box, in one transaction.
func (s *Store) SendParcel(ctx context.Context, r domain.ParcelSend) (domain.ParcelSendResult, error) {
	recipient := strings.TrimSpace(r.Recipient)
	if len(r.OperationID) < 8 || recipient == "" || len(recipient) > 32 || r.Mesos < 0 || r.Mesos > domain.ParcelMaxMesos || len(r.Message) > 4*domain.ParcelMaxMessage {
		return domain.ParcelSendResult{}, domain.ErrInvalidInput
	}
	if r.InvType < 0 || r.InvType > 5 || (r.InvType == 0 && r.Mesos == 0) {
		return domain.ParcelSendResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ParcelSendResult{}, err
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, r.Session)
	if err != nil {
		return domain.ParcelSendResult{}, err
	}
	if c.Revision != r.Session.Character.Revision {
		return domain.ParcelSendResult{}, domain.ErrStaleOwner
	}
	c.HP, c.MP = r.Session.Character.HP, r.Session.Character.MP
	before := c
	var receiverID, receiverAccount int64
	err = tx.QueryRowContext(ctx, `SELECT id,account_id FROM game_characters WHERE name_key=$1 AND world_id=$2 FOR UPDATE`, strings.ToLower(recipient), c.WorldID).Scan(&receiverID, &receiverAccount)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ParcelSendResult{}, domain.ErrParcelRecipient
	}
	if err != nil {
		return domain.ParcelSendResult{}, err
	}
	if receiverAccount == c.AccountID {
		return domain.ParcelSendResult{}, domain.ErrParcelSameAccount
	}
	waiting, err := readParcels(ctx, tx, `SELECT `+parcelColumns+` FROM asset_parcels WHERE receiver_id=$1 AND expires_at>clock_timestamp()`, receiverID)
	if err != nil {
		return domain.ParcelSendResult{}, err
	}
	if len(waiting) >= domain.ParcelCapacity {
		return domain.ParcelSendResult{}, domain.ErrParcelBoxFull
	}
	result := domain.ParcelSendResult{Inventory: domain.InventoryResult{Character: c}}
	cost := r.Mesos + domain.ParcelFee(r.Mesos, r.Quick)
	if cost > result.Inventory.Character.Mesos {
		return domain.ParcelSendResult{}, domain.ErrInsufficient
	}
	result.Inventory.Character.Mesos -= cost
	result.Cost = cost
	if r.Quick {
		if err = takeParcelCoupon(ctx, tx, &result.Inventory, r.CouponSlot); err != nil {
			return domain.ParcelSendResult{}, err
		}
	}
	parcel := domain.Parcel{SenderID: c.ID, SenderName: c.Name, ReceiverID: receiverID, Mesos: r.Mesos, Quick: r.Quick}
	if r.Quick {
		parcel.Message = r.Message
	}
	if r.InvType > 0 {
		held := findParcelItem(result.Inventory.Character, r.InvType, r.Slot)
		if held == nil {
			return domain.ParcelSendResult{}, domain.ErrNotFound
		}
		definition, err := inventoryDefinition(held.templateID)
		// Cash items keep a serial in asset_cash_items; parcels do not carry it.
		if err != nil || definition.Cash || held.cashID > 0 || !definition.Tradeable(0) {
			return domain.ParcelSendResult{}, domain.ErrForbidden
		}
		if definition.Only {
			for _, p := range waiting {
				if parcelTemplate(p) == held.templateID {
					return domain.ParcelSendResult{}, domain.ErrParcelOnlyOne
				}
			}
		}
		quantity := r.Quantity
		if r.InvType == 1 {
			quantity = 1
		} else if kind := held.templateID / 10000; kind == 207 || kind == 233 {
			// Stars and bullets go as the whole stack, as Legacy sends them.
			quantity = held.quantity
		}
		taken, err := takeInventoryItems(ctx, tx, &result.Inventory, []domain.TradeItem{{InvType: r.InvType, Slot: r.Slot, Quantity: quantity, InstanceID: held.id, TradeSlot: 1}}, true)
		if err != nil {
			return domain.ParcelSendResult{}, err
		}
		parcel.Equipment, parcel.Item = taken[0].Equipment, taken[0].Item
		if parcel.Item != nil {
			parcel.Item.ID, parcel.Item.Slot, parcel.Item.Revision = 0, 0, 0
		}
	}
	var equipment []byte
	invType, templateID, quantity := 0, 0, 0
	if parcel.Equipment != nil {
		if equipment, err = json.Marshal(parcel.Equipment); err != nil {
			return domain.ParcelSendResult{}, err
		}
		invType, templateID, quantity = 1, parcel.Equipment.ItemID, 1
	} else if parcel.Item != nil {
		invType, templateID, quantity = parcel.Item.InvType, parcel.Item.TemplateID, parcel.Item.Quantity
	}
	delay := domain.ParcelDelay()
	if r.Quick {
		delay = 0
	}
	// One clock reading, so arrival and expiry are exact offsets of the send.
	if err = tx.QueryRowContext(ctx, `WITH t AS (SELECT clock_timestamp() AS now) INSERT INTO asset_parcels(world_id,receiver_id,sender_id,sender_account_id,sender_name,mesos,inv_type,template_id,quantity,equipment,quick,message,sent_at,arrives_at,expires_at) SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,t.now,t.now+make_interval(secs => $13::float8),t.now+make_interval(secs => $13::float8+$14::float8) FROM t RETURNING id,sent_at,arrives_at,expires_at`,
		c.WorldID, receiverID, c.ID, c.AccountID, c.Name, r.Mesos, invType, templateID, quantity, nullableJSON(equipment), r.Quick, parcel.Message, delay.Seconds(), parcelInterval()).Scan(&parcel.ID, &parcel.SentAt, &parcel.ArrivesAt, &parcel.ExpiresAt); err != nil {
		return domain.ParcelSendResult{}, err
	}
	if err = commitInventoryState(ctx, tx, &result.Inventory.Character); err != nil {
		return domain.ParcelSendResult{}, err
	}
	if err = appendEconomicLedger(ctx, tx, "parcel/"+r.OperationID, before, result.Inventory.Character, "parcel_send"); err != nil {
		return domain.ParcelSendResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.ParcelSendResult{}, err
	}
	s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	result.Parcel = parcel
	return result, nil
}

// TakeParcel gives the receiver a parcel's mesos and item, or deletes it.
func (s *Store) TakeParcel(ctx context.Context, r domain.ParcelTake) (domain.ParcelTakeResult, error) {
	if len(r.OperationID) < 8 || r.ParcelID <= 0 {
		return domain.ParcelTakeResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ParcelTakeResult{}, err
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, r.Session)
	if err != nil {
		return domain.ParcelTakeResult{}, err
	}
	if c.Revision != r.Session.Character.Revision {
		return domain.ParcelTakeResult{}, domain.ErrStaleOwner
	}
	c.HP, c.MP = r.Session.Character.HP, r.Session.Character.MP
	before := c
	parcel, err := scanParcel(tx.QueryRowContext(ctx, `SELECT `+parcelColumns+` FROM asset_parcels WHERE id=$1 AND receiver_id=$2 FOR UPDATE`, r.ParcelID, c.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ParcelTakeResult{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ParcelTakeResult{}, err
	}
	result := domain.ParcelTakeResult{Inventory: domain.InventoryResult{Character: c}, Parcel: parcel}
	if !r.Delete {
		var arrived, alive bool
		if err = tx.QueryRowContext(ctx, `SELECT $1::timestamptz<=clock_timestamp(), $2::timestamptz>clock_timestamp()`, parcel.ArrivesAt, parcel.ExpiresAt).Scan(&arrived, &alive); err != nil {
			return domain.ParcelTakeResult{}, err
		}
		if !alive {
			return domain.ParcelTakeResult{}, domain.ErrNotFound
		}
		if !arrived {
			return domain.ParcelTakeResult{}, domain.ErrParcelInTransit
		}
		if template := parcelTemplate(parcel); template > 0 {
			definition, err := inventoryDefinition(template)
			if err != nil {
				return domain.ParcelTakeResult{}, err
			}
			if definition.Only && holdsTemplate(c, template) {
				return domain.ParcelTakeResult{}, domain.ErrParcelOnlyOne
			}
		}
		next := c.Mesos + parcel.Mesos
		if next < c.Mesos || next > domain.MaxMoney() {
			return domain.ParcelTakeResult{}, domain.ErrInsufficient
		}
		result.Inventory.Character.Mesos = next
		if parcel.Equipment != nil || parcel.Item != nil {
			item := domain.TradeItem{Equipment: parcel.Equipment, Item: parcel.Item}
			if parcel.Item != nil {
				item.InvType, item.Quantity = parcel.Item.InvType, parcel.Item.Quantity
			} else {
				item.InvType, item.Quantity = 1, 1
			}
			if err = receiveTradeItem(ctx, tx, &result.Inventory, item); err != nil {
				if errors.Is(err, domain.ErrInsufficient) {
					return domain.ParcelTakeResult{}, domain.ErrParcelNoRoom
				}
				return domain.ParcelTakeResult{}, err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM asset_parcels WHERE id=$1`, parcel.ID); err != nil {
		return domain.ParcelTakeResult{}, err
	}
	if !r.Delete {
		if err = commitInventoryState(ctx, tx, &result.Inventory.Character); err != nil {
			return domain.ParcelTakeResult{}, err
		}
		if err = appendEconomicLedger(ctx, tx, "parcel/"+r.OperationID, before, result.Inventory.Character, "parcel_receive"); err != nil {
			return domain.ParcelTakeResult{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return domain.ParcelTakeResult{}, err
	}
	if !r.Delete {
		s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	}
	return result, nil
}

// parcelInterval is the parcel lifetime in seconds for make_interval.
func parcelInterval() float64 {
	return domain.ParcelLifetime.Seconds()
}

func parcelTemplate(p domain.Parcel) int {
	if p.Equipment != nil {
		return p.Equipment.ItemID
	}
	if p.Item != nil {
		return p.Item.TemplateID
	}
	return 0
}

func holdsTemplate(c domain.Character, template int) bool {
	for _, e := range c.Equipment {
		if e.ItemID == template {
			return true
		}
	}
	for _, item := range c.Items {
		if item.TemplateID == template {
			return true
		}
	}
	return false
}

type heldItem struct {
	id         int64
	templateID int
	quantity   int
	cashID     int64
}

// findParcelItem is the item the parcel window offers from one inventory
// slot. The client sends the tab and slot, not the item, so the slot decides.
func findParcelItem(c domain.Character, invType, slot int) *heldItem {
	if invType == 1 {
		for _, e := range c.Equipment {
			if e.Slot == slot && slot > 0 {
				return &heldItem{id: e.ID, templateID: e.ItemID, quantity: 1, cashID: e.CashID}
			}
		}
		return nil
	}
	for _, item := range c.Items {
		if item.InvType == invType && item.Slot == slot && slot > 0 {
			return &heldItem{id: item.ID, templateID: item.TemplateID, quantity: item.Quantity, cashID: item.CashID}
		}
	}
	return nil
}

// takeParcelCoupon uses one 快遞使用券 from the cash inventory slot the client
// named. commitInventoryState retires the serial of a stack used up.
func takeParcelCoupon(ctx context.Context, tx *sql.Tx, result *domain.InventoryResult, slot int) error {
	c := &result.Character
	for i, item := range c.Items {
		if item.InvType != 5 || item.Slot != slot || item.TemplateID != domain.ParcelQuickItem || item.Quantity < 1 {
			continue
		}
		if err := guardItemExpiry(ctx, tx, item.ExpiresAt); err != nil {
			return err
		}
		if item.Quantity == 1 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM asset_items WHERE id=$1`, item.ID); err != nil {
				return err
			}
			c.Items = append(c.Items[:i], c.Items[i+1:]...)
			result.Changes = append(result.Changes, domain.InventoryChange{Mode: 3, InvType: 5, Slot: slot})
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE asset_items SET quantity=quantity-1,revision=revision+1 WHERE id=$1`, item.ID); err != nil {
			return err
		}
		c.Items[i].Quantity--
		c.Items[i].Revision++
		result.Changes = append(result.Changes, domain.InventoryChange{Mode: 1, InvType: 5, Slot: slot, Quantity: c.Items[i].Quantity})
		return nil
	}
	return domain.ErrNotFound
}
