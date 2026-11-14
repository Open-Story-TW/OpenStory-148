package world

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
	"maple-go-server/scripts"
)

type mapEntryRequest struct {
	token     uint64
	session   domain.CharacterSession
	channel   int
	sink      packetSink
	oldScript *worldScript
	name      string
}
type sceneTicket struct {
	request             mapEntryRequest
	scene               content.MapScene
	notBefore, deadline time.Time
	claimed             bool
}
type mapEntryQueue struct {
	mu      sync.Mutex
	token   uint64
	pending *mapEntryRequest
	running bool
	scene   *sceneTicket
}

func mapQueue(live *liveCharacter) *mapEntryQueue {
	live.mu.Lock()
	defer live.mu.Unlock()
	if live.mapEntry == nil {
		live.mapEntry = &mapEntryQueue{}
	}
	return live.mapEntry
}
func (s *Server) invalidateMapEntry(live *liveCharacter) {
	q := mapQueue(live)
	invalidateMapEntryQueue(q)
}
func invalidateMapEntryQueue(q *mapEntryQueue) {
	if q == nil {
		return
	}
	q.mu.Lock()
	q.token++
	q.pending = nil
	q.scene = nil
	q.mu.Unlock()
}
func (s *Server) enqueueMapEntry(channel int, live *liveCharacter, sink packetSink) {
	if s.gameplay == nil || s.scripts == nil {
		return
	}
	live.mu.Lock()
	snapshot := live.session
	live.mu.Unlock()
	m, err := s.gameplay.catalog.Map(snapshot.Character.MapID)
	name := ""
	// Add each reviewed entry independently; a WZ name never grants arbitrary
	// filesystem access or makes all legacy map scripts executable.
	if err == nil && m.ID == 2010000 && m.OnUserEnter == "goLith" {
		name = "map/on_user_enter/goLith.lua"
	}
	s.scripts.mu.Lock()
	old := s.scripts.active[snapshot.CharacterID]
	if old != nil && (old.live != live || old.identity.SessionID != snapshot.SessionID || old.identity.Generation != snapshot.Generation) {
		old = nil
	}
	s.scripts.mu.Unlock()
	q := mapQueue(live)
	q.mu.Lock()
	q.token++
	q.scene = nil
	q.pending = nil
	if name != "" {
		q.pending = &mapEntryRequest{token: q.token, session: snapshot, channel: channel, sink: sink, oldScript: old, name: name}
	}
	start := q.pending != nil && !q.running
	if start {
		q.running = true
	}
	q.mu.Unlock()
	if start {
		go s.runMapEntries(live, q)
	}
}
func (s *Server) mapEntryCurrent(live *liveCharacter, q *mapEntryQueue, request mapEntryRequest) bool {
	q.mu.Lock()
	same := q.token == request.token
	q.mu.Unlock()
	if !same {
		return false
	}
	live.mu.Lock()
	a, b := live.session, request.session
	live.mu.Unlock()
	if a.SessionID != b.SessionID || a.Generation != b.Generation || a.CharacterID != b.CharacterID || a.Lease.InstanceID != b.Lease.InstanceID || a.Lease.Epoch != b.Lease.Epoch || a.ChannelID != request.channel || a.Character.MapID != b.Character.MapID {
		return false
	}
	s.mu.RLock()
	current, ok := s.online[b.CharacterID]
	s.mu.RUnlock()
	return ok && current.SessionID == b.SessionID && current.Generation == b.Generation && current.Lease.InstanceID == b.Lease.InstanceID && current.Lease.Epoch == b.Lease.Epoch && current.ChannelID == request.channel && current.Character.MapID == b.Character.MapID
}
func (s *Server) runMapEntries(live *liveCharacter, q *mapEntryQueue) {
	for {
		q.mu.Lock()
		request := q.pending
		q.pending = nil
		if request == nil {
			q.running = false
			q.mu.Unlock()
			return
		}
		q.mu.Unlock()
		if !s.mapEntryCurrent(live, q, *request) {
			continue
		}
		// A warp may originate while the old Lua VM/a.mu is held. Wait here,
		// holding no character, persistence, map, queue or script-table lock.
		if old := request.oldScript; old != nil {
			old.mu.Lock()
			s.scripts.mu.Lock()
			if s.scripts.active[request.session.CharacterID] == old {
				delete(s.scripts.active, request.session.CharacterID)
			}
			s.scripts.mu.Unlock()
			if old.session != nil {
				old.session.Cancel()
			}
			old.mu.Unlock()
		}
		if !s.mapEntryCurrent(live, q, *request) {
			continue
		}
		if _, err := scripts.Source(request.name); err != nil {
			continue
		}
		id := scripting.Identity{SessionID: request.session.SessionID, CharacterID: request.session.CharacterID, Generation: request.session.Generation}
		a := &worldScript{server: s, live: live, sink: request.sink, kind: "map", identity: id}
		options := scripting.StartOptions{API: "map", Host: &mapEntryHost{server: s, live: live, queue: q, request: *request}}
		q.mu.Lock()
		if q.token != request.token {
			q.mu.Unlock()
			continue
		}
		s.scripts.mu.Lock()
		// A newly opened NPC owns its own invocation; never cancel it because
		// an older map-entry worker finally woke up.
		if s.scripts.active[id.CharacterID] != nil {
			s.scripts.mu.Unlock()
			q.mu.Unlock()
			continue
		}
		if arm, ok := s.scripts.armed[id.CharacterID]; ok && arm.identity == id {
			options.Debug = arm.options.Debug
			options.Breakpoints = arm.options.Breakpoints
			delete(s.scripts.armed, id.CharacterID)
		}
		s.scripts.active[id.CharacterID] = a
		s.scripts.mu.Unlock()
		q.mu.Unlock()
		a.mu.Lock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		session, event, err := s.scripts.engine.Start(ctx, id, request.name, options)
		a.session = session
		if err != nil {
			_ = a.fail(err)
		} else {
			_ = a.deliver(event)
		}
		cancel()
		a.mu.Unlock()
	}
}

type mapEntryHost struct {
	server  *Server
	live    *liveCharacter
	queue   *mapEntryQueue
	request mapEntryRequest
}

func (h *mapEntryHost) Execute(ctx context.Context, id scripting.Identity, command scripting.Command) (scripting.CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return scripting.CommandResult{}, err
	}
	r := h.request
	if id.SessionID != r.session.SessionID || id.CharacterID != r.session.CharacterID || id.Generation != r.session.Generation || command.Script != r.name || !h.server.mapEntryCurrent(h.live, h.queue, r) {
		return scripting.CommandResult{}, scripting.ErrIdentity
	}
	h.live.mu.Lock()
	snapshot := h.live.session
	h.live.mu.Unlock()
	if command.Kind == "gender" && len(command.Ints) == 0 && command.Text == "" {
		return scripting.CommandResult{Value: int64(snapshot.Character.Gender)}, nil
	}
	if command.Kind != "play_scene" || len(command.Ints) != 0 || snapshot.Character.HP <= 0 || snapshot.Character.MapID != 2010000 || r.name != "map/on_user_enter/goLith.lua" || command.Text != fmt.Sprintf("Effect/Direction3.img/goLith/Scene%d", snapshot.Character.Gender) {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	plan, err := content.LoadMapScene(os.Getenv("MAPLEGO_WZ_ROOT"), command.Text)
	if err != nil || plan.Transition.TargetMap != 104000000 {
		return scripting.CommandResult{}, domain.ErrInvalidInput
	}
	if _, err = h.server.gameplay.catalog.Map(plan.Transition.TargetMap); err != nil {
		return scripting.CommandResult{}, err
	}
	h.live.persistMu.Lock()
	defer h.live.persistMu.Unlock()
	if err := ctx.Err(); err != nil {
		return scripting.CommandResult{}, err
	}
	if !h.server.mapEntryCurrent(h.live, h.queue, r) {
		return scripting.CommandResult{}, scripting.ErrIdentity
	}
	h.live.mu.Lock()
	alive := h.live.session.Character.HP > 0
	h.live.mu.Unlock()
	if !alive {
		return scripting.CommandResult{}, domain.ErrForbidden
	}
	now := time.Now()
	ticket := &sceneTicket{request: r, scene: plan, notBefore: now.Add(plan.Transition.After - 250*time.Millisecond), deadline: now.Add(plan.Transition.After + 10*time.Second)}
	h.queue.mu.Lock()
	if h.queue.token != r.token || h.queue.scene != nil {
		h.queue.mu.Unlock()
		return scripting.CommandResult{}, scripting.ErrIdentity
	}
	h.queue.scene = ticket
	h.queue.mu.Unlock()
	for _, packet := range [][]byte{protocol.SceneUILock(true), protocol.SceneKeyLock(true), protocol.SceneEffect(plan.Path)} {
		if err = h.request.sink.WritePacket(packet); err != nil {
			h.queue.mu.Lock()
			if h.queue.scene == ticket {
				h.queue.scene = nil
			}
			h.queue.mu.Unlock()
			return scripting.CommandResult{}, err
		}
	}
	time.AfterFunc(time.Until(ticket.deadline), func() {
		h.live.persistMu.Lock()
		defer h.live.persistMu.Unlock()
		h.queue.mu.Lock()
		expired := h.queue.scene == ticket && !ticket.claimed
		if expired {
			h.queue.scene = nil
		}
		h.queue.mu.Unlock()
		if expired && h.server.mapEntryCurrent(h.live, h.queue, r) {
			_ = r.sink.WritePacket(protocol.SceneUILock(false))
			_ = r.sink.WritePacket(protocol.SceneKeyLock(false))
		}
	})
	return scripting.CommandResult{Value: true}, nil
}
func (s *Server) sceneTransfer(ctx context.Context, channel int, live *liveCharacter, sink packetSink, request protocol.PortalRequest) (bool, error) {
	if request.TargetMap == -1 || request.Name != "" {
		return false, nil
	}
	q := mapQueue(live)
	q.mu.Lock()
	ticket := q.scene
	q.mu.Unlock()
	if ticket == nil {
		return false, nil
	}
	live.mu.Lock()
	alive := live.session.Character.HP > 0
	live.mu.Unlock()
	if !alive {
		// A dead character's request is the death dialog's return, never
		// a scene transfer: a pending scene ticket (e.g. from login while
		// already dead) must not swallow it.
		return false, nil
	}
	valid := request.Revival != nil && request.Revival.WheelType == 0 && request.TargetMap == ticket.scene.Transition.TargetMap && channel == ticket.request.channel && sameDropSink(sink, ticket.request.sink) && s.mapEntryCurrent(live, q, ticket.request)
	now := time.Now()
	q.mu.Lock()
	valid = valid && q.scene == ticket && !ticket.claimed && !now.Before(ticket.notBefore) && !now.After(ticket.deadline)
	if valid {
		ticket.claimed = true
	}
	q.mu.Unlock()
	if !valid {
		return true, sink.WritePacket(protocol.EnableActions())
	}
	defer func() {
		q.mu.Lock()
		if q.scene == ticket {
			ticket.claimed = false
		}
		q.mu.Unlock()
	}()
	m, err := s.gameplay.catalog.Map(request.TargetMap)
	if err != nil {
		return true, err
	}
	entry, ok := m.Spawn()
	if !ok {
		return true, domain.ErrInvalidInput
	}
	guard := func(current domain.CharacterSession) bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		r := ticket.request
		return q.scene == ticket && q.token == r.token && ticket.claimed && time.Now().Before(ticket.deadline) && current.SessionID == r.session.SessionID && current.Generation == r.session.Generation && current.CharacterID == r.session.CharacterID && current.Lease.InstanceID == r.session.Lease.InstanceID && current.Lease.Epoch == r.session.Lease.Epoch && current.ChannelID == r.channel && current.Character.MapID == r.session.Character.MapID && current.Character.HP > 0
	}
	err = s.warpGameplayConditional(ctx, channel, live, sink, m.ID, entry, nil, guard, [][]byte{protocol.SceneUILock(false), protocol.SceneKeyLock(false)})
	return true, err
}
