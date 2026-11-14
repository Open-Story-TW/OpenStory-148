package world

import (
	"context"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

// publishScriptTravel publishes a destination already committed atomically
// with its fare/token. Caller retains persistMu through all field publication.
func (a *worldScript) publishScriptTravel(ctx context.Context, snapshot domain.CharacterSession, result domain.ScriptExchangeResult, expected *sceneVisit) (scripting.CommandResult, error) {
	before := snapshot.Character
	published := snapshot
	published.Character = result.Character
	beforeField := func() error {
		if len(result.Changes) > 0 {
			if err := a.sink.WritePacket(protocol.ModifyInventory(result.Changes)); err != nil {
				return err
			}
		}
		return a.sink.WritePacket(protocol.UpdateMesos(result.Character.Mesos, true))
	}
	if err := a.server.publishGameplayWarp(ctx, snapshot.ChannelID, a.live, a.sink, before, published, false, expected, beforeField, false); err != nil {
		return scripting.CommandResult{}, err
	}
	return scripting.CommandResult{Value: map[string]any{"ok": true}}, nil
}
