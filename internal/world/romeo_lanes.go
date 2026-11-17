package world

import (
	"context"
	"fmt"
	"math/rand/v2"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

const romeoLaneRows = 5

type romeoLabLanes struct {
	columns  [romeoLaneRows][4]int
	owners   [4]int64
	progress [4]int
}

func newRomeoLabLanes() *romeoLabLanes {
	lanes := &romeoLabLanes{}
	for row := range lanes.columns {
		for lane, column := range rand.Perm(4) {
			lanes.columns[row][lane] = column
		}
	}
	return lanes
}

func (s *Server) romeoLanePortal(ctx context.Context, inst *eventInstance, actor eventOccupant, session domain.CharacterSession, portal content.Portal) (bool, error) {
	first := inst.definition.Maps[0]
	current := session.Character.MapID
	switch {
	case current == first+300:
		var laneNumber int
		switch portal.Script {
		case "rnj4_r1", "jnr4_r1":
			laneNumber = 1
		case "rnj4_r2", "jnr4_r2":
			laneNumber = 2
		case "rnj4_r3", "jnr4_r3":
			laneNumber = 3
		case "rnj4_r4", "jnr4_r4":
			laneNumber = 4
		default:
			return false, nil
		}
		return true, s.enterRomeoLane(ctx, inst, actor, session, laneNumber)
	case current >= first+301 && current <= first+304:
		lane := current - first - 301
		switch {
		case portal.Script == "rnj5_rp" || portal.Script == "jnr5_rp":
			return true, s.climbRomeoLane(ctx, inst, actor, session, lane, portal.Name)
		case portal.Name == "out00" || portal.Script == "rnj_out300" || portal.Script == "jnr_out300":
			s.events.mu.Lock()
			finished := inst.romeoLanes != nil && inst.romeoLanes.owners[lane] == session.CharacterID &&
				inst.romeoLanes.progress[lane] == romeoLaneRows
			s.events.mu.Unlock()
			if !finished {
				return true, eventPortalNotice(actor.sink, "請先通過這一列的五層傳送點。")
			}
			return true, s.romeoWarpNamed(ctx, inst, actor, current, first+400, "st00")
		}
	case current == first+400 && (portal.Script == "rnj12_in" || portal.Script == "jnr12_in"):
		return true, s.enterRomeoBoss(ctx, inst, actor, session)
	}
	return false, nil
}

func (s *Server) enterRomeoLane(ctx context.Context, inst *eventInstance, actor eventOccupant, session domain.CharacterSession, laneNumber int) error {
	lane := laneNumber - 1
	s.events.mu.Lock()
	if inst.romeoLanes == nil {
		inst.romeoLanes = newRomeoLabLanes()
	}
	lanes := inst.romeoLanes
	owner := lanes.owners[lane]
	if owner != 0 && owner != session.CharacterID {
		s.events.mu.Unlock()
		return eventPortalNotice(actor.sink, "已有隊員在這個入口，請選另一列。")
	}
	lanes.owners[lane] = session.CharacterID
	s.events.mu.Unlock()

	err := s.romeoWarpNamed(ctx, inst, actor, session.Character.MapID, inst.definition.Maps[0]+300+laneNumber, "sp")
	if err != nil && owner == 0 {
		s.events.mu.Lock()
		lanes.owners[lane] = 0
		s.events.mu.Unlock()
	}
	return err
}

func (s *Server) climbRomeoLane(ctx context.Context, inst *eventInstance, actor eventOccupant, session domain.CharacterSession, lane int, portal string) error {
	if len(portal) != 4 || portal[:2] != "pt" || portal[2] < '0' || portal[2] >= '0'+romeoLaneRows ||
		portal[3] < '0' || portal[3] > '3' {
		return eventPortalNotice(actor.sink, "這個傳送點暫時無法使用。")
	}
	row, column := int(portal[2]-'0'), int(portal[3]-'0')
	s.events.mu.Lock()
	lanes := inst.romeoLanes
	if lanes == nil || lanes.owners[lane] != session.CharacterID || lanes.progress[lane] != row {
		s.events.mu.Unlock()
		return eventPortalNotice(actor.sink, "請使用目前所在樓層的傳送點。")
	}
	correct := lanes.columns[row][lane] == column
	s.events.mu.Unlock()
	next := row + 1
	message := "通過這一層。"
	if !correct {
		next = max(0, row-1)
		message = "這個傳送點通往較低的樓層。"
	}
	target := fmt.Sprintf("pt%d0", next)
	if next == romeoLaneRows {
		target = "success"
	} else if next == 0 {
		target = "fail"
	}
	current := session.Character.MapID
	if err := s.romeoWarpNamed(ctx, inst, actor, current, current, target); err != nil {
		return err
	}
	s.events.mu.Lock()
	lanes.progress[lane] = next
	s.events.mu.Unlock()
	return eventPortalNotice(actor.sink, message)
}

func (s *Server) romeoWarpNamed(ctx context.Context, inst *eventInstance, actor eventOccupant, from, to int, name string) error {
	target, err := s.gameplay.catalog.Map(to)
	if err != nil {
		return err
	}
	entry, ok := target.Portal(name)
	if !ok {
		return domain.ErrNotFound
	}
	return s.warpEventMember(ctx, inst, from, actor, to, entry)
}
