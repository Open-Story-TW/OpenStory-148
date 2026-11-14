package world

import (
	"context"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// guildCapacityPoll is how often the world looks for a changed ceiling or
// base capacity; the settings themselves are read every five seconds.
const guildCapacityPoll = 2 * time.Second

// runGuildCapacity resends the guild record to every online member when the
// operator changes guild_max_capacity or guild_base_capacity, so the guild
// window shows the new capacity without a relog. The full record (mode 0x20)
// is used instead of the capacity change (mode 64), which would open the
// guild office NPC's dialog on the guild master's screen. A changed
// buddy_capacity likewise goes to every online character as friend mode 21.
func (s *Server) runGuildCapacity(ctx context.Context) {
	ticker := time.NewTicker(guildCapacityPoll)
	defer ticker.Stop()
	last, lastBuddy := guildCapacityKey(), domain.BuddyCapacitySetting()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if key := guildCapacityKey(); key != last {
				last = key
				s.pushGuildRecords(ctx)
			}
			if buddy := domain.BuddyCapacitySetting(); buddy != lastBuddy {
				lastBuddy = buddy
				s.pushBuddyCapacity()
			}
		}
	}
}

// pushBuddyCapacity sends each online character its friend list size.
func (s *Server) pushBuddyCapacity() {
	if s.buddies == nil {
		return
	}
	s.buddies.mu.Lock()
	defer s.buddies.mu.Unlock()
	for _, peer := range s.buddies.peers {
		if packet := protocol.BuddyCapacity(domain.EffectiveBuddyCapacity(peer.list.Capacity)); packet != nil {
			_ = peer.sink.WritePacket(packet)
		}
	}
}

func guildCapacityKey() [2]int {
	return [2]int{domain.GuildCapacityLimit(), domain.GuildBaseCapacity()}
}

// pushGuildRecords sends each online guild's current record to its online
// members in this world.
func (s *Server) pushGuildRecords(ctx context.Context) {
	repo, ok := s.repo.(domain.GuildRepository)
	if !ok {
		return
	}
	space := s.guildSpace()
	space.mu.Lock()
	peers := make([]guildPeer, 0, len(space.peers))
	for _, p := range space.peers {
		peers = append(peers, p)
	}
	space.mu.Unlock()
	readers := map[int64]*liveCharacter{}
	for _, p := range peers {
		p.live.mu.Lock()
		id := p.live.session.Character.Guild.ID
		p.live.mu.Unlock()
		if id != 0 && readers[id] == nil {
			readers[id] = p.live
		}
	}
	for _, live := range readers {
		call, done := context.WithTimeout(ctx, 3*time.Second)
		g, err := repo.ReadGuild(call, s.petSession(live))
		done()
		if err != nil || g.ID == 0 {
			continue
		}
		space.commands.Lock()
		space.deliver(g.Members, 0, protocol.GuildInfo(g, space.online))
		space.commands.Unlock()
	}
}
