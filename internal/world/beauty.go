package world

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"os"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

// styleFilter keeps the hair/face IDs the game data has, in order, so the
// avatar preview never asks the client to draw a missing style.
func styleFilter(command scripting.Command) (scripting.CommandResult, error) {
	if len(command.Ints) == 0 || len(command.Ints) > 255 {
		return scripting.CommandResult{}, domain.ErrInvalidInput
	}
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	kept := make([]int64, 0, len(command.Ints))
	for _, id := range command.Ints {
		if id >= 20000 && id <= 49999 && content.StyleExists(root, int(id)) {
			kept = append(kept, id)
		}
	}
	return scripting.CommandResult{Value: kept}, nil
}

// changeLook is Legacy cm.setAvatar (one style) and cm.setRandomAvatar (a
// server draw among several). Ints are coupon, kind (1 hair, 2 face) and the
// candidate styles. The coupon and the new look commit in one exchange.
func (a *worldScript) changeLook(ctx context.Context, command scripting.Command) (scripting.CommandResult, error) {
	if len(command.Ints) < 3 || len(command.Ints) > 257 || command.Ints[0]/10000 != 515 {
		return scripting.CommandResult{}, domain.ErrInvalidInput
	}
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	kind := command.Ints[1]
	candidates := make([]int, 0, len(command.Ints)-2)
	for _, id := range command.Ints[2:] {
		hair := id >= 30000 && id <= 49999
		face := id >= 20000 && id <= 29999
		if kind == 1 && !hair || kind == 2 && !face || kind != 1 && kind != 2 {
			return scripting.CommandResult{}, domain.ErrInvalidInput
		}
		if content.StyleExists(root, int(id)) {
			candidates = append(candidates, int(id))
		}
	}
	if len(candidates) == 0 {
		return scripting.CommandResult{Value: map[string]any{"ok": false, "reason": "no_style"}}, nil
	}
	pick := candidates[0]
	if len(candidates) > 1 {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(candidates))))
		if err != nil {
			return scripting.CommandResult{}, err
		}
		pick = candidates[n.Int64()]
	}
	look := domain.ScriptLook{Coupon: int(command.Ints[0])}
	if kind == 1 {
		look.Hair = pick
	} else {
		look.Face = pick
	}
	repo, ok := a.server.repo.(domain.ScriptRepository)
	if !ok {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	if a.server.tradeActive(a.live) {
		return scripting.CommandResult{Value: map[string]any{"ok": false, "reason": "trade_active"}}, nil
	}
	a.live.persistMu.Lock()
	defer a.live.persistMu.Unlock()
	a.live.mu.Lock()
	snapshot := a.live.session
	snapshot.Lease = a.server.currentLease()
	a.live.mu.Unlock()
	value, err := repo.ExecuteScriptExchange(ctx, domain.ScriptExchange{Session: snapshot, OperationID: command.ID, Look: &look})
	if errors.Is(err, domain.ErrInsufficient) {
		return scripting.CommandResult{Value: map[string]any{"ok": false, "reason": "no_coupon"}}, nil
	}
	if err != nil {
		return scripting.CommandResult{}, err
	}
	a.server.applyInventoryResult(a.live, value.Character)
	a.live.mu.Lock()
	a.live.session.Character.Hair, a.live.session.Character.Face = value.Character.Hair, value.Character.Face
	current := a.live.session.Character
	a.live.mu.Unlock()
	if len(value.Changes) > 0 {
		if err = a.sink.WritePacket(protocol.ModifyInventory(value.Changes)); err != nil {
			return scripting.CommandResult{}, err
		}
	}
	if err = a.sink.WritePacket(protocol.UpdateLookStats(current, look.Face != 0, look.Hair != 0)); err != nil {
		return scripting.CommandResult{}, err
	}
	a.server.refreshCharacterLook(a.live)
	return scripting.CommandResult{Value: map[string]any{"ok": true, "style": int64(pick)}}, nil
}
