package world

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

const (
	monsterRushNPC      = 9010002
	monsterRushHub      = 120000000
	monsterRushGoal     = int64(700_000_000_000)
	monsterRushDelay    = 5 * time.Minute
	monsterRushDuration = 20 * time.Minute
	monsterRushMob      = 3110300
	monsterRushHP       = 800_000_000
)

var monsterRushAmounts = []int64{69, 50_000_000, 100_000_000, 200_000_000, 400_000_000, 800_000_000, 1_500_000_000}

type monsterRushAnchor struct {
	mapID int
	x     int
	y     int
	fh    int
}

var monsterRushAnchors = []monsterRushAnchor{
	{260000000, 108, 275, 13}, {680000000, 2037, 193, 297},
	{211000000, -1463, 94, 156}, {600000000, 2031, 501, 210},
	{120000000, 1180, 305, 24},
}

type monsterRushSpawn struct {
	room *gameplayRoom
	life content.Life
}

func (a *worldScript) monsterRush(ctx context.Context, snapshot domain.CharacterSession, command scripting.Command) (scripting.CommandResult, error) {
	repo, ok := a.server.repo.(domain.ScriptRepository)
	if !ok || a.npcID != monsterRushNPC || snapshot.Character.MapID != monsterRushHub || a.server.gameplay == nil {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	fund, schedule := domain.MonsterRushLogKeys(snapshot.ChannelID)
	g := a.server.gameplay
	g.mu.Lock()
	_, _, readyErr := g.monsterRushSpawnsLocked(snapshot.ChannelID)
	g.mu.Unlock()
	if readyErr != nil {
		return scripting.CommandResult{Value: map[string]any{"unavailable": true}}, nil
	}
	remaining := func(values []domain.ScriptLogValue) int64 {
		if values[1].Value == 0 {
			return max(0, monsterRushGoal-values[0].Value)
		}
		if time.Now().Before(time.Unix(values[1].Value, 0).Add(monsterRushDuration)) {
			return 0
		}
		return monsterRushGoal
	}
	if command.Kind == "monster_rush_status" {
		values, err := repo.ReadScriptLogs(ctx, snapshot, []domain.ScriptLogKey{fund, schedule})
		if err != nil {
			return scripting.CommandResult{}, err
		}
		return scripting.CommandResult{Value: map[string]any{"remaining": remaining(values)}}, nil
	}
	if len(command.Ints) != 1 || !slices.Contains(monsterRushAmounts, command.Ints[0]) {
		return scripting.CommandResult{}, domain.ErrInvalidInput
	}
	rejected := func(reason string) (scripting.CommandResult, error) {
		return scripting.CommandResult{Value: map[string]any{"paid": int64(0), "reason": reason}}, nil
	}
	a.live.persistMu.Lock()
	defer a.live.persistMu.Unlock()
	snapshot = a.server.skillSession(a.live)
	if snapshot.Character.MapID != monsterRushHub {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	for attempt := 0; attempt < 3; attempt++ {
		values, err := repo.ReadScriptLogs(ctx, snapshot, []domain.ScriptLogKey{fund, schedule})
		if err != nil {
			return scripting.CommandResult{}, err
		}
		left := remaining(values)
		if left == 0 {
			return rejected("closed")
		}
		paid := min(command.Ints[0], left)
		collected := monsterRushGoal - left + paid
		start := int64(0)
		if paid == left {
			start = time.Now().Add(monsterRushDelay).Unix()
		}
		r, err := repo.ExecuteScriptExchange(ctx, domain.ScriptExchange{
			Session: snapshot, OperationID: command.ID, MesosDelta: -paid,
			Logs: []domain.ScriptLogChange{
				{Key: fund, Set: &collected, Expected: &values[0].Value},
				{Key: schedule, Set: &start, Expected: &values[1].Value},
			},
		})
		if errors.Is(err, domain.ErrInsufficient) {
			return rejected("insufficient")
		}
		if errors.Is(err, domain.ErrAlreadyExists) {
			continue
		}
		if err != nil {
			return scripting.CommandResult{}, err
		}
		a.server.applyInventoryResult(a.live, r.Character)
		if !r.Replayed {
			_ = a.sink.WritePacket(protocol.UpdateMesos(r.Character.Mesos, true))
			if start != 0 {
				a.server.broadcastNotice(snapshot.ChannelID, 0, "怪物來襲已達募款目標，五分鐘後開始。")
			}
		}
		return scripting.CommandResult{Value: map[string]any{"paid": paid}}, nil
	}
	return rejected("changed")
}

// runMonsterRush consumes the durable start time once within this world lease.
// A prior process may have already awarded kills from an older wave.
func (s *Server) runMonsterRush(ctx context.Context) {
	repo, ok := s.repo.(domain.MonsterRushScheduleRepository)
	if !ok || s.gameplay == nil {
		return
	}
	boot := time.Now().Unix()
	started := make(map[int]int64)
	poll := time.NewTicker(2 * time.Second)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-poll.C:
			s.gameplay.removeMonsterRushMobs(now)
			readCtx, cancel := context.WithTimeout(ctx, time.Second)
			schedules, err := repo.MonsterRushSchedules(readCtx, s.cfg.WorldID)
			cancel()
			if err != nil {
				slog.Warn("monster_rush_schedule_read_failed", "error", err)
				continue
			}
			for channel, stamp := range schedules {
				deadline := time.Unix(stamp, 0).Add(monsterRushDuration)
				if channel > s.wcfg.ChannelCount || stamp < boot || stamp <= started[channel] || now.Unix() < stamp || !deadline.After(now) {
					continue
				}
				if err := s.gameplay.spawnMonsterRush(channel, now, deadline); err != nil {
					slog.Warn("monster_rush_spawn_failed", "channel", channel, "error", err)
					continue
				}
				started[channel] = stamp
				s.broadcastNotice(channel, 0, "怪物開始來襲！請到阿里安特、亞邁斯、冰原雪域、新葉城或鯨魚號迎戰。")
			}
		}
	}
}

func (g *gameplaySpace) monsterRushSpawnsLocked(channel int) (content.Mob, []monsterRushSpawn, error) {
	stats, err := g.catalog.Mob(monsterRushMob)
	if err != nil {
		return stats, nil, err
	}
	stats.HP = monsterRushHP
	entries := make([]monsterRushSpawn, 0, len(monsterRushAnchors))
	for _, a := range monsterRushAnchors {
		room, err := g.room(mapKey{channel, a.mapID})
		if err != nil {
			return stats, nil, err
		}
		found := false
		for _, f := range room.definition.Footholds {
			if f.ID != a.fh || a.x < min(f.X1, f.X2) || a.x > max(f.X1, f.X2) || f.X1 == f.X2 {
				continue
			}
			y := f.Y1 + (a.x-f.X1)*(f.Y2-f.Y1)/(f.X2-f.X1)
			if y < a.y-2 || y > a.y+2 {
				break
			}
			entries = append(entries, monsterRushSpawn{room, content.Life{
				Kind: "m", TemplateID: monsterRushMob, X: a.x, Y: y, Foothold: a.fh,
				RX0: min(f.X1, f.X2), RX1: max(f.X1, f.X2), RespawnSeconds: -1,
			}})
			found = true
			break
		}
		if !found {
			return stats, nil, fmt.Errorf("monster rush foothold %d absent from map %d at (%d,%d)", a.fh, a.mapID, a.x, a.y)
		}
	}
	return stats, entries, nil
}

func (g *gameplaySpace) spawnMonsterRush(channel int, now, deadline time.Time) error {
	g.mu.Lock()
	stats, entries, err := g.monsterRushSpawnsLocked(channel)
	if err != nil {
		g.mu.Unlock()
		return err
	}
	if int64(g.nextID)+int64(len(entries))*39 >= 999999999 {
		g.mu.Unlock()
		return errors.New("monster rush object ID space exhausted")
	}
	var deliveries []roomDelivery
	for _, entry := range entries {
		count := 1 + rand.IntN(39)
		for i := 0; i < count; i++ {
			oid := g.alloc()
			mob := &gameplayMob{objectID: oid, life: entry.life, origin: entry.life,
				stats: stats, hp: stats.HP, rushUntil: deadline}
			entry.room.mobs[oid] = mob
			deliveries = append(deliveries, roomBroadcast(entry.room, mob.spawnPacket(false, now), -1)...)
			for id, member := range entry.room.members {
				mob.controller = id
				deliveries = append(deliveries, roomDelivery{member.sink, mob.spawnPacket(true, now)})
				break
			}
		}
	}
	g.mu.Unlock()
	deliver(deliveries)
	return nil
}

func (g *gameplaySpace) removeMonsterRushMobs(now time.Time) {
	g.mu.Lock()
	var deliveries []roomDelivery
	for key, room := range g.rooms {
		if !slices.ContainsFunc(monsterRushAnchors, func(anchor monsterRushAnchor) bool { return anchor.mapID == key.mapID }) {
			continue
		}
		for id, mob := range room.mobs {
			if mob.rushUntil.IsZero() || mob.claiming || mob.reservation != nil {
				continue
			}
			if mob.hp <= 0 || !mob.rushUntil.After(now) {
				if mob.hp > 0 {
					deliveries = append(deliveries, roomBroadcast(room, protocol.KillMonster(id), -1)...)
				}
				delete(room.mobs, id)
			}
		}
	}
	g.mu.Unlock()
	deliver(deliveries)
}
