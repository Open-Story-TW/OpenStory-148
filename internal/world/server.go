package world

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	maplev1 "maple-go-server/api/maplev1"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
	servicerpc "maple-go-server/internal/rpc"
)

type Server struct {
	maplev1.UnimplementedWorldControlServer
	cfg              platform.Config
	wcfg             platform.WorldConfig
	repo             domain.Repository
	mu               sync.RWMutex
	sceneMu          sync.Mutex
	sceneGates       map[int64]*scenePublicationGate
	scenes           map[int64]*sceneVisit
	lease            domain.WorldLease
	online           map[int64]domain.CharacterSession
	maps             *mapSpace
	drops            *dropSpace
	channelTransfers map[channelTransferKey]*channelTransfer
	orphanSince      map[orphanKey]time.Time
	gameplay         *gameplaySpace
	trades           *tradeSpace
	economyDialogs   *economyDialogSpace
	merchants        *merchantSpace
	scripts          *scriptWorld
	skills           *skillSpace
	buddies          *buddySpace
	parties          *partySpace
	events           *eventSpace
	transitStart     time.Time
	elevators        *elevatorSpace
	quizzes          *oxQuizSpace
	hak              *hakSpace
	boatInvaders     map[mapKey]*boatInvasion // Only the transit ticker changes this map.
	boatPhases       map[mapKey]transitPhase  // Only the transit ticker changes this map.
	guilds           *guildSpace
	guildOnce        sync.Once
	ready            atomic.Bool
}

// liveCharacter is the connection-local aggregate. Move updates stay in memory;
// progress checkpoints flush through SaveCharacter with fencing.
type liveCharacter struct {
	mu               sync.Mutex
	persistMu        sync.Mutex // serialize checkpoints and durable asset commands
	rps              *rpsGame   // guarded by persistMu
	nativeHandle     int64
	god              bool // GM !god; guarded by mu, lost on relog or channel change.
	session          domain.CharacterSession
	movedAt          time.Time                 // when the client last reported session.Character.X/Y; guarded by mu
	longDamage       protocol.LongDamageLedger // Owned by the session's read loop.
	statDetail       []byte                    // Last 0x614 frame sent; guarded by mu.
	panelClicks      int                       // Demo panel presses this session; guarded by mu.
	dirty            bool
	saving           bool
	chat             chatBudget
	mapEntry         *mapEntryQueue
	visit            *sceneVisit
	petHungerAt      time.Time              // next pet hunger pass; zero while no pet is summoned
	guildGPAt        time.Time              // next hourly guild presence GP grant
	fatigueAt        time.Time              // when the profession fatigue was last worn off; guarded by mu
	attackKey        atomic.Uint32          // last settlement key handed out by nextAttackKey
	itemUseUncertain *domain.UseItemRequest // persistMu; exact-owner unresolved item commit
	scrollUncertain  *domain.ScrollRequest  // persistMu; exact-owner unresolved scroll commit
}

// nextAttackKey is the settlement key of one attack (OperationID and the
// client_tick uniqueness column): strictly increasing per live character, so
// two attacks never share one. The client's own tick cannot be the key:
// keydown attacks (Hurricane 3121004, Ishtar's Ring 23121000, Rapid Fire
// 5221004) repeat the keydown's tick on every frame, which made every frame
// after the first a "duplicate" (attack_rate) or a settlement collision
// (already exists) while the client kept drawing damage.
func (l *liveCharacter) nextAttackKey(now time.Time) uint32 {
	for {
		last := l.attackKey.Load()
		key := uint32(now.UnixMilli())
		if key <= last {
			key = last + 1
		}
		if l.attackKey.CompareAndSwap(last, key) {
			return key
		}
	}
}

func Run(parent context.Context, cfg platform.Config, repo domain.Repository, health *platform.Health) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	wcfg, _ := cfg.World(cfg.WorldID)
	lease, err := platform.AcquireLease(ctx, "world", platform.LeaseWait, func(ctx context.Context) (domain.WorldLease, error) {
		return repo.AcquireWorldLease(ctx, cfg.WorldID, cfg.InstanceID)
	})
	if err != nil {
		return err
	}
	gameplay, err := newGameplaySpace()
	if err != nil {
		return err
	}
	scripts, err := newScriptWorld()
	if err != nil {
		return err
	}
	s := &Server{cfg: cfg, wcfg: wcfg, repo: repo, lease: lease, online: map[int64]domain.CharacterSession{}, maps: newMapSpace(DefaultAOIRadius), drops: newDropSpace(), gameplay: gameplay, trades: newTradeSpace(), economyDialogs: newEconomyDialogSpace(), scripts: scripts, skills: newSkillSpace()}
	s.buddies = newBuddySpace()
	if _, ok := repo.(domain.MerchantRepository); ok {
		s.merchants = newMerchantSpace()
		if err = s.loadMerchants(ctx); err != nil {
			return err
		}
	}
	s.parties = newPartySpace()
	s.events = newEventSpace()
	s.transitStart = time.Now()
	s.elevators = &elevatorSpace{starts: make(map[mapKey]time.Time)}
	s.quizzes = newOXQuizSpace()
	s.hak = &hakSpace{seats: make(map[int]*hakSeat)}
	s.boatInvaders = make(map[mapKey]*boatInvasion)
	s.boatPhases = make(map[mapKey]transitPhase)
	if cfg.ScriptsDir != "" {
		// A bad file on disk must not stop the world: it is reported and the
		// built-in source keeps running.
		if report, err := s.reloadScripts(); err != nil {
			slog.Error("script_directory_unreadable", "dir", cfg.ScriptsDir, "error", err)
		} else {
			slog.Info("script_directory_loaded", "dir", cfg.ScriptsDir, "changed", len(report.Changed), "rejected", report.Rejected)
		}
	}
	health.Handle("/admin/scripts/reload", http.HandlerFunc(s.scriptReload))
	health.Handle("/debug/scripts", http.HandlerFunc(s.scriptDebugger))
	health.Handle("/admin/gm-grants", http.HandlerFunc(s.gmAuthorizations))
	health.Handle("/admin/qa-mobs/snapshot", http.HandlerFunc(s.qaMobSnapshot))
	if qaAdminCommandsEnabled() {
		health.Handle("/admin/gm-command", http.HandlerFunc(s.gmCommandAdmin))
	}
	health.Handle("/admin/pets", http.HandlerFunc(s.petAdmin))
	health.Handle("/admin/qa-family", http.HandlerFunc(s.qaFamilyAdmin))
	health.Handle("/admin/monitor/players", http.HandlerFunc(s.monitorPlayers))
	health.Handle("/admin/monitor/events", http.HandlerFunc(s.monitorEvents))
	health.Handle("/admin/monitor/notice", http.HandlerFunc(s.monitorNotice))
	health.Handle("/admin/characters", http.HandlerFunc(s.adminCharacter))
	health.Handle("/admin/characters/inventory", http.HandlerFunc(s.adminInventory))
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		_ = repo.ReleaseWorldLease(cleanup, s.currentLease())
	}()
	rpcListener, err := net.Listen("tcp", wcfg.RPCAddr)
	if err != nil {
		return err
	}
	rpcServer := grpc.NewServer(grpc.UnaryInterceptor(servicerpc.AuthInterceptor(cfg.RPCToken)), grpc.MaxRecvMsgSize(64*1024), grpc.MaxSendMsgSize(512*1024))
	maplev1.RegisterWorldControlServer(rpcServer, s)
	defer rpcServer.Stop()
	go func() {
		if err := rpcServer.Serve(rpcListener); err != nil && ctx.Err() == nil {
			slog.Error("world_rpc_failed", "world", cfg.WorldID, "error", err)
			cancel()
		}
	}()
	g := platform.NewTCPGroup()
	defer g.Close()
	for channel := 1; channel <= wcfg.ChannelCount; channel++ {
		channelID := channel
		for _, host := range cfg.ListenHosts() {
			address := net.JoinHostPort(host, strconv.Itoa(wcfg.ChannelBasePort+channel-1))
			if err = g.Listen(ctx, address, func(ctx context.Context, c net.Conn) { s.handle(ctx, c, channelID) }); err != nil {
				return err
			}
		}
	}
	s.ready.Store(true)
	health.Ready(true)
	health.Detail("world_id", cfg.WorldID)
	health.Detail("channel_count", wcfg.ChannelCount)
	health.Detail("channel_base_port", wcfg.ChannelBasePort)
	health.Detail("rpc", wcfg.RPCAddr)
	health.Detail("epoch", lease.Epoch)
	// Optional in-process dummies (build tag loaddummy + MAPLEGO_LOAD_DUMMY=1).
	s.startLoadDummies(ctx)
	go s.runGameplay(ctx)
	go s.runMonsterRush(ctx)
	go s.runTransit(ctx)
	go s.runSkills(ctx)
	go s.runCombatLoot(ctx)
	go s.runCombatSettlements(ctx)
	go s.runGuildCapacity(ctx)
	if s.merchants != nil {
		go s.runMerchants(ctx)
	}
	go s.renew(ctx, cancel, health)
	<-ctx.Done()
	s.ready.Store(false)
	health.Ready(false)
	return nil
}

func (s *Server) currentLease() domain.WorldLease { s.mu.RLock(); defer s.mu.RUnlock(); return s.lease }
func (s *Server) renew(ctx context.Context, cancel context.CancelFunc, health *platform.Health) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.expireChannelTransfers(ctx)
			reclaimCtx, reclaimDone := context.WithTimeout(ctx, 3*time.Second)
			s.reclaimOrphanSessions(reclaimCtx, time.Now())
			reclaimDone()
			lease := s.currentLease()
			renewCtx, done := context.WithTimeout(ctx, 3*time.Second)
			next, err := s.repo.RenewWorldLease(renewCtx, lease)
			done()
			if err != nil {
				slog.Warn("world_lease_renew_failed", "world", s.cfg.WorldID, "error", err)
				s.ready.Store(false)
				health.Ready(false)
				if errors.Is(err, domain.ErrStaleOwner) || time.Now().After(lease.ExpiresAt.Add(-2*time.Second)) {
					cancel()
					return
				}
				continue
			}
			s.mu.Lock()
			s.lease = next
			count := len(s.online)
			s.mu.Unlock()
			s.ready.Store(true)
			health.Ready(true)
			health.Detail("online", count)
		}
	}
}

func (s *Server) Status(ctx context.Context, _ *maplev1.StatusRequest) (*maplev1.StatusResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return &maplev1.StatusResponse{WorldId: int32(s.cfg.WorldID), InstanceId: s.cfg.InstanceID, Epoch: s.lease.Epoch, Ready: s.ready.Load(), Online: int32(len(s.online))}, nil
}

func (s *Server) CreateCharacter(ctx context.Context, req *maplev1.CreateCharacterRequest) (*maplev1.CreateCharacterResponse, error) {
	if int(req.WorldId) != s.cfg.WorldID {
		return nil, status.Error(codes.PermissionDenied, "wrong world")
	}
	if !s.ready.Load() {
		return nil, status.Error(codes.Unavailable, "world not ready")
	}
	if len(req.OperationId) < 16 {
		return nil, status.Error(codes.InvalidArgument, "missing operation ID")
	}
	if len(req.LoginSessionId) < 16 || req.LoginGeneration < 1 {
		return nil, status.Error(codes.Unauthenticated, "missing authenticated login session")
	}
	chr, err := s.repo.CreateCharacter(ctx, domain.CreateCharacterRequest{Lease: s.currentLease(), LoginSession: domain.LoginSession{AccountID: req.AccountId, SessionID: req.LoginSessionId, Generation: req.LoginGeneration}, AccountID: req.AccountId, WorldID: int(req.WorldId), Name: req.Name, Gender: int(req.Gender), Skin: int(req.Skin), Face: int(req.Face), Hair: int(req.Hair), Top: int(req.Top), Bottom: int(req.Bottom), Shoes: int(req.Shoes), Weapon: int(req.Weapon), Family: int(req.Family), SubJob: int(req.SubJob), OperationID: req.OperationId})
	if err != nil {
		code := codes.Internal
		if errors.Is(err, domain.ErrInvalidInput) {
			code = codes.InvalidArgument
		}
		if errors.Is(err, domain.ErrAlreadyExists) {
			code = codes.AlreadyExists
		}
		if errors.Is(err, domain.ErrForbidden) {
			code = codes.PermissionDenied
		}
		return nil, status.Error(code, err.Error())
	}
	body, err := json.Marshal(chr)
	if err != nil {
		return nil, status.Error(codes.Internal, "character serialization failed")
	}
	return &maplev1.CreateCharacterResponse{CharacterJson: body}, nil
}

func (s *Server) handle(ctx context.Context, raw net.Conn, channel int) {
	c, err := protocol.NewServerConn(raw)
	if err != nil {
		return
	}
	defer c.Close()
	c.SetTimeouts(30*time.Second, 5*time.Second)
	var live *liveCharacter
	var traceID int64
	fullTrace := false
	handedOff := false
	unimplemented := make(map[uint16]bool)
	attackSamples := 0
	checkpointStop := make(chan struct{})
	var checkpointWG sync.WaitGroup
	defer func() {
		close(checkpointStop)
		checkpointWG.Wait()
		if live == nil || handedOff {
			return
		}
		s.tradeLeave(live)
		s.merchantLeave(live)
		s.leaveBuddies(live)
		s.leaveParty(live)
		s.leaveGuild(live)
		s.closeEconomyDialog(live)
		s.cancelNPCScript(live)
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		live.mu.Lock()
		needSave := live.dirty
		session := live.session
		live.mu.Unlock()
		if needSave {
			if err := s.checkpoint(cleanup, live); err != nil {
				slog.Warn("character_disconnect_save_failed", "character", session.CharacterID, "error", err)
			}
		}
		live.mu.Lock()
		session = live.session
		live.mu.Unlock()
		s.leaveScene(live, c)
		if err := s.repo.ReleaseCharacter(cleanup, session); err != nil {
			slog.Warn("character_release_failed", "character", session.CharacterID, "error", err)
		}
		slog.Info("character_left_world", "world", s.cfg.WorldID, "channel", channel, "character", session.CharacterID, "revision", session.Character.Revision)
	}()
	for {
		packet, err := c.ReadPacket()
		if err != nil {
			if traceID > 0 {
				slog.Info("trace_read_ended", "character", traceID, "error", err)
			}
			return
		}
		if traceID > 0 {
			recordCharacterPacket(slog.Default(), "trace_in", traceID, fullTrace, packet)
		}
		if live != nil {
			packet = live.longDamage.Absorb(packet)
		}
		r := protocol.NewReader(packet)
		op := r.U16()
		if live != nil && s.sceneFor(live, c) == nil {
			if traceID > 0 {
				slog.Info("trace_scene_binding_lost", "character", traceID, "opcode", op)
			}
			return
		}
		switch op {
		case protocol.RecvHello:
			if r.U8() != 6 || r.U16() != protocol.Version || r.U16() != 1 || r.Err() != nil {
				return
			}
			if c.WritePacket(protocol.ShowMapleStory()) != nil {
				return
			}
		case protocol.RecvLoggedIn:
			if live != nil || !s.ready.Load() {
				return
			}
			handle := int64(r.I32())
			if r.Err() != nil || handle <= 0 {
				return
			}
			ip, _, err := net.SplitHostPort(raw.RemoteAddr().String())
			if err != nil {
				return
			}
			claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			next, matched, err := s.consumeChannel(claimCtx, handle, channel, ip)
			var session domain.CharacterSession
			freshLogin := false
			if !matched {
				if repo, ok := s.repo.(domain.CashRepository); ok {
					session, matched, err = repo.ConsumeCashReturn(claimCtx, handle, s.cfg.WorldID, channel, ip, s.currentLease())
				}
				if !matched && err == nil {
					session, err = s.repo.ConsumeTransfer(claimCtx, domain.TransferClaim{ClientHandle: handle, WorldID: s.cfg.WorldID, ChannelID: channel, ClientIP: ip, Lease: s.currentLease(), AllowLegacyLoopback: s.cfg.AllowLegacyLoopback})
					freshLogin = true
				}
				next = &liveCharacter{session: session, nativeHandle: handle}
			} else if err == nil {
				session = next.session
			}
			cancel()
			if err != nil {
				slog.Warn("world_handoff_rejected", "world", s.cfg.WorldID, "channel", channel, "error", err)
				return
			}
			live = next
			if traceCharacterEnabled(session.CharacterID) {
				traceID = session.CharacterID
				fullTrace = os.Getenv("MAPLEGO_TRACE_FULL_PACKETS") == "1"
			}
			traceCharacterPackets(c, session.CharacterID)
			c.SetTimeouts(15*time.Minute, 5*time.Second)
			if err = s.expireCashBeforeField(ctx, live); err != nil {
				return
			}
			if freshLogin && !domain.RelogBuffsEnabled() {
				s.dropRelogBuffs(ctx, live)
			}
			live.mu.Lock()
			session = live.session
			live.mu.Unlock()
			if err = s.publishInitialScene(ctx, live, c, channel); err != nil {
				return
			}
			checkpointWG.Add(1)
			go func(current *liveCharacter) {
				defer checkpointWG.Done()
				s.runCheckpoints(ctx, checkpointStop, current, c)
			}(live)
			slog.Info("character_entered_world", "world", s.cfg.WorldID, "channel", channel, "character", session.CharacterID, "map", session.Character.MapID, "epoch", session.Lease.Epoch)
		case protocol.RecvPong, protocol.RecvClientStart:
			// No state changes and no database reads for these packets.
		case protocol.RecvKeymap:
			if live == nil {
				return
			}
			if err = s.changeKeymap(ctx, live, r); err != nil {
				return
			}
		case protocol.RecvClientError:
			slog.Warn("client_reported_error", "world", s.cfg.WorldID, "channel", channel, "bytes", r.Remaining())
		case protocol.RecvMove:
			if live == nil {
				return
			}
			// A detached copy: movement commands outside the reviewed set (casterMove
			// rushes such as Aran 21100002) drop the packet, not the client.
			body := r.Bytes(r.Remaining())
			if err := s.moveScene(live, c, protocol.NewReader(body)); err != nil {
				if !errors.Is(err, protocol.ErrMalformed) {
					return
				}
				if moveParseDiag.Add(1) <= 50 {
					slog.Warn("move_parse_rejected", "character", liveCharacterID(live), "hex", hex.EncodeToString(body))
				}
			}

		case protocol.RecvMesoDrop:
			if live == nil {
				return
			}
			if s.tradeActive(live) {
				_ = c.WritePacket(protocol.EnableActions())
				break
			}
			request, err := protocol.ReadMesoDrop(r)
			if err != nil {
				return
			}
			amount := request.Amount
			if amount < 10 || amount > 50000 {
				_ = c.WritePacket(protocol.EnableActions())
				break
			}
			dropCtx, done := context.WithTimeout(ctx, 3*time.Second)
			handled, err := s.dropPlayerMesos(dropCtx, live, c, request.Tick, int64(amount))
			done()
			if err != nil {
				return
			}
			if handled {
				break
			}
			live.mu.Lock()
			live.session.Lease = s.currentLease()
			session := live.session
			live.mu.Unlock()
			if int64(amount) > session.Character.Mesos {
				_ = c.WritePacket(protocol.EnableActions())
				break
			}
			objectID := s.drops.AllocObjectID()
			if objectID <= 0 {
				_ = c.WritePacket(protocol.EnableActions())
				break
			}
			dropOp := s.drops.nextOperationID("drop-meso", session.CharacterID, objectID)
			pickupOp := s.drops.nextOperationID("pickup-meso", session.CharacterID, objectID)
			saveCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			updated, err := s.adjustMesos(saveCtx, live, -int64(amount), dropOp, "player_drop")
			cancel()
			if err != nil {
				slog.Warn("meso_drop_failed", "character", session.CharacterID, "error", err)
				_ = c.WritePacket(protocol.EnableActions())
				break
			}
			live.mu.Lock()
			snapshot := live.session
			live.mu.Unlock()
			s.updateOnlineOwned(snapshot, snapshot)
			s.drops.RegisterMeso(channel, snapshot.Character.MapID, objectID, amount, snapshot.CharacterID, snapshot.Character.X, snapshot.Character.Y, dropOp, pickupOp)
			_ = c.WritePacket(protocol.UpdateMesos(updated.Mesos, true))
		case protocol.RecvItemPickup:
			if live == nil {
				return
			}
			if s.tradeActive(live) {
				_ = c.WritePacket(protocol.EnableActions())
				break
			}
			r.Skip(4) // tick
			r.Skip(1)
			_ = r.I16()
			_ = r.I16() // client pos
			objectID := r.I32()
			if r.Err() != nil || objectID <= 0 {
				return
			}
			handled, err := s.handleInventoryPickup(ctx, live, c, objectID)
			if err != nil {
				return
			}
			if handled {
				break
			}
			live.mu.Lock()
			live.session.Lease = s.currentLease()
			session := live.session
			live.mu.Unlock()
			amount, _, pickupOp, ok := s.drops.ClaimMeso(channel, session.Character.MapID, objectID, session.Character.X, session.Character.Y)
			if !ok {
				_ = c.WritePacket(protocol.EnableActions())
				break
			}
			saveCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			updated, err := s.adjustMesos(saveCtx, live, int64(amount), pickupOp, "meso_pickup")
			cancel()
			if err != nil {
				s.drops.ReleaseClaim(channel, session.Character.MapID, objectID)
				slog.Warn("meso_pickup_failed", "character", session.CharacterID, "error", err)
				_ = c.WritePacket(protocol.EnableActions())
				break
			}
			live.mu.Lock()
			// Keep live progress fields; meso ledger already bumped revision.
			snapshot := live.session
			live.mu.Unlock()
			s.updateOnlineOwned(snapshot, snapshot)
			s.drops.Remove(channel, snapshot.Character.MapID, objectID, int32(snapshot.CharacterID))
			_ = c.WritePacket(protocol.UpdateMesos(updated.Mesos, true))
			_ = c.WritePacket(protocol.ShowMesoGain(int64(amount)))
		case protocol.RecvInventoryMove:
			if live == nil {
				return
			}
			if s.tradeActive(live) {
				_ = c.WritePacket(protocol.EnableActions())
				break
			}
			if err := s.handleInventoryMove(ctx, live, c, r); err != nil {
				return
			}
		case protocol.RecvAttributeAllocate, protocol.RecvAttributeAutoAllocate:
			if live == nil {
				return
			}
			attributeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			var err error
			if op == protocol.RecvAttributeAutoAllocate {
				err = s.handleAttributeAutoAllocation(attributeCtx, live, c, r)
			} else {
				err = s.handleAttributeAllocation(attributeCtx, live, c, r)
			}
			cancel()
			if err != nil {
				return
			}
		case protocol.RecvBuddy:
			if live == nil {
				return
			}
			if err = s.handleBuddy(ctx, live, c, r); err != nil {
				return
			}
		case protocol.RecvParty:
			if live == nil {
				return
			}
			if err = s.handleParty(ctx, live, c, r); err != nil {
				return
			}
		case protocol.RecvExpedition:
			if live == nil {
				return
			}
			if err = s.handleExpedition(ctx, live, c, r); err != nil {
				return
			}
		case protocol.RecvPartyReply:
			if live == nil {
				return
			}
			if err = s.handlePartyReply(ctx, live, c, r); err != nil {
				return
			}
		case protocol.RecvSpawnPet, protocol.RecvPetFood, protocol.RecvPetCommand, protocol.RecvMovePet, protocol.RecvPetChat:
			if live == nil {
				return
			}
			if err = s.handlePetPacket(ctx, live, c, op, r); err != nil {
				return
			}
		case protocol.RecvAlliance, protocol.RecvAllianceDeny, protocol.RecvMultiChat:
			if live == nil {
				return
			}
			switch op {
			case protocol.RecvAlliance:
				err = s.handleAlliance(ctx, live, c, r)
			case protocol.RecvAllianceDeny:
				err = s.mutateAlliance(ctx, live, c, domain.AllianceRequest{Action: domain.AllianceDeny})
			default:
				err = s.handleMultiChat(ctx, live, c, r)
			}
			if err != nil {
				return
			}
		case protocol.RecvGuild, protocol.RecvGuildDeny:
			if live == nil {
				return
			}
			if op == protocol.RecvGuild {
				err = s.handleGuild(ctx, live, c, r)
			} else {
				err = s.handleGuildDeny(ctx, live, c, r)
			}
			if err != nil {
				return
			}
		case protocol.RecvGeneralChat:
			if live == nil {
				return
			}
			if err := s.handleGeneralChat(ctx, live, c, r); err != nil {
				return
			}
		case protocol.RecvUseUpgradeScroll:
			if live == nil {
				return
			}
			scrollCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := s.handleScroll(scrollCtx, live, c, r)
			cancel()
			if err != nil {
				return
			}
		case protocol.RecvUsePotentialScroll, protocol.RecvUseMagnify:
			if live == nil {
				return
			}
			potentialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := s.handlePotentialCommand(potentialCtx, live, c, op, r)
			cancel()
			if err != nil {
				return
			}
		case protocol.RecvUseItem:
			if live == nil {
				return
			}
			if s.tradeActive(live) {
				_ = c.WritePacket(protocol.EnableActions())
				break
			}
			req, err := protocol.ReadUseItem(r)
			if err != nil {
				return
			}
			useCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err = s.handleItemUse(useCtx, live, c, req)
			cancel()
			if err != nil {
				slog.Warn("item_use_disconnect", "world", s.cfg.WorldID, "character", live.session.CharacterID, "item", req.TemplateID, "slot", req.Slot, "error", err)
				return
			}
		case protocol.RecvChangeChannel:
			if live == nil {
				return
			}
			if s.tradeActive(live) {
				_ = c.WritePacket(protocol.EnableActions())
				break
			}
			target := int(r.U8()) + 1
			r.U32() // native client tick
			if r.Err() != nil || r.Remaining() != 0 {
				return
			}
			ip, _, err := net.SplitHostPort(raw.RemoteAddr().String())
			if err != nil {
				return
			}
			transferCtx, done := context.WithTimeout(ctx, 3*time.Second)
			err = s.prepareChannel(transferCtx, live, target, ip)
			done()
			if err != nil {
				if c.WritePacket(protocol.ServerBlocked(1)) != nil || c.WritePacket(protocol.EnableActions()) != nil {
					return
				}
				break
			}
			s.leaveScene(live, c)
			handedOff = true
			s.cancelNPCScript(live)
			s.tradeLeave(live)
			s.merchantLeave(live)
			s.closeEconomyDialog(live)
			if c.WritePacket(protocol.ChannelChange(domain.AdvertisedHost(raw.LocalAddr(), s.wcfg.Host), s.wcfg.ChannelBasePort+target-1)) == nil {
				awaitRedirectDisconnect(c)
			}
			return
		case protocol.RecvEnterCashshop, protocol.RecvEnterITC:
			if live == nil {
				return
			}
			if s.tradeActive(live) {
				_ = c.WritePacket(protocol.EnableActions())
				break
			}
			ip, _, err := net.SplitHostPort(raw.RemoteAddr().String())
			if err != nil {
				return
			}
			transferCtx, done := context.WithTimeout(ctx, 5*time.Second)
			err = s.prepareCashshop(transferCtx, live, ip, op == protocol.RecvEnterITC)
			done()
			if err != nil {
				if c.WritePacket(protocol.ServerBlocked(2)) != nil || c.WritePacket(protocol.EnableActions()) != nil {
					return
				}
				break
			}
			handedOff = true
			s.cancelNPCScript(live)
			s.tradeLeave(live)
			s.merchantLeave(live)
			s.closeEconomyDialog(live)
			s.leaveScene(live, c)
			if c.WritePacket(protocol.ChannelChange(domain.AdvertisedHost(raw.LocalAddr(), s.cfg.Bind), s.cfg.CashshopPort)) == nil {
				awaitRedirectDisconnect(c)
			}
			return
		case protocol.RecvPlayerInteraction:
			if live == nil {
				return
			}
			if handled, err := s.handleMerchantInteraction(ctx, live, c, r); handled {
				if err != nil {
					return
				}
				break
			}
			if err := s.handleTrade(ctx, live, c, r); err != nil {
				return
			}
		case protocol.RecvEntrustedShopCheck:
			if live == nil {
				return
			}
			if err := s.handleMerchantCheck(ctx, live, c); err != nil {
				return
			}
		case protocol.RecvStoreBank:
			if live == nil {
				return
			}
			if err := s.handleStoreBank(ctx, live, c, r); err != nil {
				return
			}
		case protocol.RecvNPCTalk:
			if live == nil {
				return
			}
			oid := r.I32()
			if r.Err() != nil || r.Remaining() > 32 {
				return
			}
			r.Skip(r.Remaining())
			handled, err := s.openNativeNPCShop(ctx, channel, live, c, oid)
			if err != nil {
				return
			}
			if handled {
				break
			}
			handled, err = s.startNPCScript(ctx, channel, live, c, oid)
			if err != nil {
				return
			}
			if !handled {
				if err = s.npcGameplay(channel, live, c, oid); err != nil {
					return
				}
			}
		case protocol.RecvQuestAction:
			if live == nil {
				return
			}
			if err := s.handleQuest(ctx, channel, live, c, r); err != nil {
				return
			}
		case protocol.RecvNPCTalkMore:
			if live == nil {
				return
			}
			handled, err := s.respondNPCScript(ctx, live, r)
			if err != nil {
				return
			}
			if !handled {
				r.Skip(r.Remaining())
				if c.WritePacket(protocol.EnableActions()) != nil {
					return
				}
			}
		case protocol.RecvMagicAttack:
			if live == nil {
				return
			}
			// Preserve a bounded native sample while validating the new family;
			// this gameplay payload cannot contain login, chat or coupon data.
			if attackSamples < 4 && len(packet) <= 514 {
				attackSamples++
				live.mu.Lock()
				characterID := live.session.CharacterID
				live.mu.Unlock()
				slog.Info("native_magic_attack_sample", "character", characterID, "bytes", len(packet)-2, "body_hex", hex.EncodeToString(packet[2:]))
			}
			if handled, genericErr := s.genericAttackRoute(ctx, channel, live, c, protocol.RecvMagicAttack, packet[2:]); handled {
				r.Skip(r.Remaining())
				if genericErr != nil {
					return
				}
				break
			}
			attack, decodeErr := protocol.ReadBeginnerMagicAttack(r)
			if decodeErr != nil {
				// Other magic families remain unavailable. Consume their packet
				// without treating it as the reviewed Three Snails layout.
				r.Skip(r.Remaining())
				// A native cast may already have predicted MP consumption. No
				// transaction ran here, so publish the current authoritative vitals.
				live.persistMu.Lock()
				live.mu.Lock()
				hp, mp := live.session.Character.HP, live.session.Character.MP
				live.mu.Unlock()
				writeErr := c.WritePacket(protocol.UpdateHPMP(hp, mp))
				live.persistMu.Unlock()
				if writeErr != nil {
					return
				}
				if c.WritePacket(protocol.EnableActions()) != nil {
					return
				}
				break
			}
			if err = s.magicGameplay(ctx, channel, live, c, attack); err != nil {
				return
			}
		case protocol.RecvStorage:
			if live == nil {
				return
			}
			if err := s.handleStorage(ctx, live, c, r); err != nil {
				return
			}
		case protocol.RecvParcel:
			if live == nil {
				return
			}
			if err := s.handleParcel(ctx, live, c, r); err != nil {
				return
			}
		case protocol.RecvNPCShop:
			if live == nil {
				return
			}
			if err := s.handleNPCShop(ctx, live, c, r); err != nil {
				return
			}
		case protocol.RecvChangeMap, protocol.RecvScriptPortal:
			if live == nil {
				return
			}
			if s.tradeActive(live) {
				_ = c.WritePacket(protocol.EnableActions())
				break
			}
			_, err := s.handleGameplay(ctx, channel, live, c, op, r)
			if err != nil {
				return
			}
		case protocol.RecvCharInfoRequest:
			if live == nil {
				return
			}
			if err := s.handleCharInfo(ctx, live, c, r); err != nil {
				return
			}
		case protocol.RecvPanel:
			if live == nil {
				return
			}
			if err := s.handlePanelClick(live, c, r); err != nil {
				return
			}
		default:
			if live == nil {
				return
			}
			handled, err := s.handleSkills(ctx, live, c, op, r)
			if err != nil {
				return
			}
			if handled {
				break
			}
			handled, err = s.handleGameplay(ctx, channel, live, c, op, r)
			if err != nil {
				return
			}
			if handled {
				break
			}
			// Keep the native request gate usable while unsupported gameplay is
			// rejected. No state/asset mutation is inferred from an unknown packet.
			if !unimplemented[op] {
				unimplemented[op] = true
				slog.Warn("world_unimplemented_opcode", "world", s.cfg.WorldID, "opcode", fmt.Sprintf("0x%x", op), "character", live.session.CharacterID)
			}
			if c.WritePacket(protocol.EnableActions()) != nil {
				return
			}
		}
		if r.Err() != nil {
			return
		}
	}
}

func (s *Server) runCheckpoints(ctx context.Context, stop <-chan struct{}, live *liveCharacter, sink packetSink) {
	live.mu.Lock()
	id := live.session.CharacterID
	live.mu.Unlock()
	// Offset by character id so checkpoints are staggered across the world.
	offset := time.Duration(id%5) * time.Second
	timer := time.NewTimer(offset + 5*time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-timer.C:
			expiryCtx, expiryDone := context.WithTimeout(ctx, 3*time.Second)
			expiryErr := s.expireCashInventory(expiryCtx, live, sink)
			expiryDone()
			if expiryErr != nil {
				slog.Warn("cash_inventory_expiry_failed", "character", id, "error", expiryErr)
			}
			guildCtx, guildDone := context.WithTimeout(ctx, 3*time.Second)
			s.tickGuild(guildCtx, live, sink, time.Now())
			guildDone()
			petCtx, petDone := context.WithTimeout(ctx, 3*time.Second)
			if petErr := s.tickPets(petCtx, live, sink, time.Now()); petErr != nil {
				slog.Warn("pet_tick_failed", "character", id, "error", petErr)
			}
			petDone()
			live.mu.Lock()
			shouldSave := live.dirty && !live.saving
			if shouldSave {
				live.saving = true
			}
			live.mu.Unlock()
			if shouldSave {
				saveCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				err := s.checkpoint(saveCtx, live)
				cancel()
				live.mu.Lock()
				live.saving = false
				charID := live.session.CharacterID
				live.mu.Unlock()
				if err != nil {
					slog.Warn("character_checkpoint_failed", "character", charID, "error", err)
				}
			}
			timer.Reset(5 * time.Second)
		}
	}
}

func (s *Server) checkpoint(ctx context.Context, live *liveCharacter) error {
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	live.session.Lease = s.currentLease()
	session := live.session
	value := live.session.Character
	live.mu.Unlock()
	if err := assetOutcomeUncertain(live, session); err != nil {
		return err
	}
	if err := s.repo.SaveCharacter(ctx, session, value); err != nil {
		return err
	}
	live.mu.Lock()
	// Only clear dirty if no newer progress arrived while the save was in flight.
	if live.session.Character.X == value.X && live.session.Character.Y == value.Y &&
		live.session.Character.MapID == value.MapID && live.session.Character.HP == value.HP &&
		live.session.Character.MP == value.MP && live.session.Character.EXP == value.EXP &&
		live.session.Character.Revision == value.Revision {
		live.dirty = false
	}
	live.session.Character.Revision = value.Revision + 1
	snapshot := live.session
	live.mu.Unlock()
	s.updateOnlineOwned(snapshot, snapshot)
	slog.Info("character_checkpoint", "world", s.cfg.WorldID, "character", snapshot.CharacterID, "revision", snapshot.Character.Revision, "x", snapshot.Character.X, "y", snapshot.Character.Y, "map", snapshot.Character.MapID)
	return nil
}
