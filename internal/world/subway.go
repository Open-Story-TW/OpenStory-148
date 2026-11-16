package world

import (
	"context"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/scripting"
)

func (a *worldScript) subwayChallenge(ctx context.Context, snapshot domain.CharacterSession, command scripting.Command) (scripting.CommandResult, error) {
	const station = 103020000
	if a.npcID != 1052007 || a.kind != "npc" || snapshot.Character.MapID != station ||
		command.Text != "" || len(command.Ints) != 1 || command.Ints[0] < 0 || command.Ints[0] > 2 {
		return scripting.CommandResult{}, domain.ErrInvalidInput
	}
	index := int(command.Ints[0])
	guard := func(session domain.CharacterSession) bool {
		return session.ChannelID == snapshot.ChannelID && session.Character.MapID == station && session.Character.HP > 0
	}
	return a.takeTransitFare(ctx, command.ID, 910360000+100*index, 4031036+index, 0, guard)
}
