package login

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	maplev1 "maple-go-server/api/maplev1"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
	servicerpc "maple-go-server/internal/rpc"
)

type Server struct {
	cfg     platform.Config
	repo    domain.Repository
	clients map[int]maplev1.WorldControlClient
}

func Run(ctx context.Context, cfg platform.Config, repo domain.Repository, health *platform.Health) error {
	s := &Server{cfg: cfg, repo: repo, clients: map[int]maplev1.WorldControlClient{}}
	for _, w := range cfg.Worlds {
		conn, err := grpc.NewClient(w.RPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return err
		}
		defer conn.Close()
		s.clients[w.ID] = maplev1.NewWorldControlClient(conn)
	}
	g := platform.NewTCPGroup()
	defer g.Close()
	for _, host := range cfg.ListenHosts() {
		if err := g.Listen(ctx, net.JoinHostPort(host, strconv.Itoa(cfg.LoginPort)), s.handle); err != nil {
			return err
		}
	}
	health.Detail("login_port", cfg.LoginPort)
	health.Detail("worlds", cfg.Worlds)
	health.Detail("protocol", "TMS148.1/locale6")
	health.Ready(true)
	<-ctx.Done()
	return nil
}

type state struct {
	account        domain.Account
	session        *domain.LoginSession
	world, channel int
	hello          bool
	redirected     bool
	attempts       int
}

func (s *Server) handle(ctx context.Context, raw net.Conn) {
	c, err := protocol.NewServerConn(raw)
	if err != nil {
		return
	}
	defer c.Close()
	// Native v145 login forms do not send packets while a user is typing.
	// Keep a bounded GUI-friendly idle window; authorization still expires
	// independently and is revalidated for every authenticated operation.
	c.SetTimeouts(15*time.Minute, 5*time.Second)
	st := state{channel: 1, world: s.cfg.Worlds[0].ID}
	defer func() {
		if st.session != nil && !st.redirected {
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = s.repo.EndLogin(cleanup, *st.session)
		}
	}()
	for {
		packet, err := c.ReadPacket()
		if err != nil {
			return
		}
		r := protocol.NewReader(packet)
		op := r.U16()
		requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = s.dispatch(requestCtx, c, &st, op, r, raw.RemoteAddr())
		cancel()
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				slog.Warn("login_request_rejected", "opcode", fmt.Sprintf("0x%x", op), "error", err)
			}
			return
		}
		if r.Err() != nil {
			return
		}
	}
}

func (s *Server) dispatch(ctx context.Context, c *protocol.Conn, st *state, op uint16, r *protocol.Reader, remote net.Addr) error {
	if requiresSession(op) {
		if st.session == nil || st.redirected {
			return domain.ErrForbidden
		}
		// Refresh only this still-valid login generation. A replaced/expired
		// socket cannot continue listing or mutating characters.
		if err := s.repo.TouchLogin(ctx, *st.session); err != nil {
			return err
		}
	}
	switch op {
	case protocol.RecvHello:
		locale, version, patch := r.U8(), r.U16(), r.U16()
		if r.Err() != nil || locale != 6 || version != protocol.Version || patch != 1 {
			return fmt.Errorf("unsupported client version")
		}
		st.hello = true
		// The launcher needs the wire contracts before any native packet.
		if err := c.WritePacket(protocol.ClientConfigPacket()); err != nil {
			return err
		}
		if err := c.WritePacket(protocol.ClientUIPacket()); err != nil {
			return err
		}
		if err := c.WritePacket(protocol.ClientFeesPacket()); err != nil {
			return err
		}
		return c.WritePacket(protocol.ShowMapleStory())
	case protocol.RecvGetServer:
		return c.WritePacket(protocol.LoginBackground())
	case protocol.RecvClientStart, protocol.RecvPong:
		return nil
	case protocol.RecvLogin:
		if !st.hello {
			return fmt.Errorf("version handshake required")
		}
		st.attempts++
		if st.attempts > 5 {
			return fmt.Errorf("login attempt limit")
		}
		name, password := protocol.ReadLoginRequest(r)
		name = strings.TrimSpace(name)
		if r.Err() != nil {
			return r.Err()
		}
		if st.session != nil {
			return fmt.Errorf("already authenticated on this connection")
		}
		account, err := s.repo.Authenticate(ctx, name, password)
		if errors.Is(err, domain.ErrNotFound) {
			account, err = s.repo.Register(ctx, name, password)
		}
		if err != nil {
			reason := byte(4)
			if !errors.Is(err, domain.ErrBadCredentials) {
				reason = 6
			}
			_ = c.WritePacket(protocol.LoginFailure(reason))
			return nil
		}
		// Reason 3 is the client's own "deleted or blocked account" notice.
		if bans, ok := s.repo.(interface {
			LoginBanned(context.Context, int64, string) (bool, error)
		}); ok {
			host, _, _ := net.SplitHostPort(remote.String())
			if banned, e := bans.LoginBanned(ctx, account.ID, host); e != nil || banned {
				reason := byte(3)
				if e != nil {
					reason = 6
				}
				_ = c.WritePacket(protocol.LoginFailure(reason))
				return nil
			}
		}
		st.account = account
		if !account.PINConfigured || account.Gender == 255 {
			return c.WritePacket(protocol.ChooseGender(account.Name))
		}
		loginSession, err := s.repo.BeginLogin(ctx, account.ID, platform.NewID())
		if err != nil {
			_ = c.WritePacket(protocol.LoginFailure(7))
			return nil
		}
		st.session = &loginSession
		if err = c.WritePacket(protocol.LoginSuccess(account)); err != nil {
			return err
		}
		return s.worldList(c)
	case protocol.RecvSetGender:
		if st.account.ID == 0 || st.account.PINConfigured {
			return fmt.Errorf("account setup not pending")
		}
		name, pin, gender := r.String(), r.String(), int(r.U8())
		if r.Err() != nil {
			return r.Err()
		}
		if !strings.EqualFold(name, st.account.Name) {
			return domain.ErrForbidden
		}
		if err := s.repo.ConfigureAccount(ctx, st.account.ID, gender, pin); err != nil {
			return err
		}
		st.account.Gender = gender
		st.account.PINConfigured = true
		return c.WritePacket(protocol.GenderSet(st.account.Name))
	case protocol.RecvRedisplay:
		if st.session == nil {
			return domain.ErrForbidden
		}
		return s.worldList(c)
	case protocol.RecvCharList:
		if st.session == nil {
			return domain.ErrForbidden
		}
		r.U8()
		world, channel := int(r.U8()), int(r.U8())+1
		if r.Err() != nil {
			return r.Err()
		}
		w, ok := s.cfg.World(world)
		if !ok || channel < 1 || channel > w.ChannelCount {
			return domain.ErrInvalidInput
		}
		st.world, st.channel = world, channel
		chars, err := s.repo.ListCharacters(ctx, st.account.ID, world)
		if err != nil {
			return err
		}
		return c.WritePacket(protocol.CharacterList(chars, domain.AdvertisedCharacterSlots(len(chars))))
	case protocol.RecvCheckName:
		if st.session == nil {
			return domain.ErrForbidden
		}
		name := r.String()
		if r.Err() != nil {
			return r.Err()
		}
		available, err := s.repo.CharacterNameAvailable(ctx, name)
		if err != nil {
			available = false
		}
		return c.WritePacket(protocol.NameResult(name, !available))
	case protocol.RecvCreate:
		if st.session == nil {
			return domain.ErrForbidden
		}
		req, err := parseCreate(r, st.account, st.world)
		if err != nil {
			return err
		}
		req.OperationID = platform.NewID()
		req.LoginSession = *st.session
		client := s.clients[st.world]
		resp, err := client.CreateCharacter(servicerpc.AuthContext(ctx, s.cfg.RPCToken), &maplev1.CreateCharacterRequest{AccountId: req.AccountID, WorldId: int32(req.WorldID), Name: req.Name, Gender: int32(req.Gender), Skin: int32(req.Skin), Face: int32(req.Face), Hair: int32(req.Hair), Top: int32(req.Top), Bottom: int32(req.Bottom), Shoes: int32(req.Shoes), Weapon: int32(req.Weapon), Family: int32(req.Family), SubJob: int32(req.SubJob), OperationId: req.OperationID, LoginSessionId: req.LoginSession.SessionID, LoginGeneration: req.LoginSession.Generation})
		if err != nil {
			// The look identifies which MakeCharInfo list a rejected native
			// creation used; none of it is a secret.
			slog.Warn("character_creation_failed", "world", st.world, "family", req.Family, "sub_job", req.SubJob, "skin", req.Skin, "look", []int{req.Face, req.Hair, req.Top, req.Bottom, req.Shoes, req.Weapon}, "error", err)
			return c.WritePacket(protocol.CreateCharacterResult(domain.Character{}, false))
		}
		var chr domain.Character
		if err = json.Unmarshal(resp.CharacterJson, &chr); err != nil {
			return err
		}
		return c.WritePacket(protocol.CreateCharacterResult(chr, true))
	case protocol.RecvSelect:
		if st.session == nil || st.redirected {
			return domain.ErrForbidden
		}
		characterID := int64(r.I32())
		if r.Err() != nil {
			return r.Err()
		}
		w, ok := s.cfg.World(st.world)
		if !ok {
			return domain.ErrInvalidInput
		}
		status, err := s.clients[st.world].Status(servicerpc.AuthContext(ctx, s.cfg.RPCToken), &maplev1.StatusRequest{})
		if err != nil || !status.Ready {
			return c.WritePacket(protocol.LoginFailure(7))
		}
		ip, _, err := net.SplitHostPort(remote.String())
		if err != nil {
			return err
		}
		transfer, err := s.repo.CreateTransfer(ctx, domain.TransferRequest{AccountID: st.account.ID, CharacterID: characterID, WorldID: st.world, ChannelID: st.channel, SessionID: st.session.SessionID, ClientIP: ip})
		if err != nil {
			return err
		}
		if transfer.ClientHandle <= 0 {
			return fmt.Errorf("missing one-use client handoff handle")
		}
		if err = c.WritePacket(protocol.ServerIP(domain.ResolveIPv4(w.Host), w.AdvertisedChannelPort(st.channel), transfer.ClientHandle)); err != nil {
			return err
		}
		st.redirected = true
		return nil
	case protocol.RecvLogout:
		return context.Canceled
	default:
		slog.Debug("login_unhandled_opcode", "opcode", fmt.Sprintf("0x%x", op))
		return nil
	}
}

func requiresSession(op uint16) bool {
	switch op {
	case protocol.RecvRedisplay, protocol.RecvCharList, protocol.RecvCheckName, protocol.RecvCreate, protocol.RecvSelect:
		return true
	default:
		return false
	}
}

func (s *Server) worldList(c *protocol.Conn) error {
	for _, w := range s.cfg.Worlds {
		if err := c.WritePacket(protocol.WorldList(w.ID, w.Name, w.ChannelCount)); err != nil {
			return err
		}
	}
	if err := c.WritePacket(protocol.EndWorldList()); err != nil {
		return err
	}
	return c.WritePacket(protocol.Recommended(s.cfg.Worlds[0].ID))
}

func parseCreate(r *protocol.Reader, a domain.Account, world int) (domain.CreateCharacterRequest, error) {
	req := domain.CreateCharacterRequest{AccountID: a.ID, WorldID: world, Name: r.String()}
	race, ok := domain.CreationRaceFor(int(r.I32()), int(r.I16()))
	req.Family, req.SubJob = race.Family, race.SubJob
	req.Gender = int(r.U8())
	req.Skin = int(r.U8())
	count := int(r.U8())
	// Client 0x591622 encodes one I32 per MakeCharInfo slot of the race's
	// family: Resistance wears an overall (no bottom), the rest six slots.
	slots := []*int{&req.Face, &req.Hair, &req.Top, &req.Bottom, &req.Shoes, &req.Weapon}
	if race.Family == 3000 {
		slots = []*int{&req.Face, &req.Hair, &req.Top, &req.Shoes, &req.Weapon}
	}
	// Phantom (v148 MakeCharInfo 2003): face, hair, overall, cape, shoes,
	// weapon. The list offers one cape, which the world equips itself.
	cape := 0
	if race.Family == 2003 {
		slots = []*int{&req.Face, &req.Hair, &req.Top, &cape, &req.Shoes, &req.Weapon}
	}
	if r.Err() != nil || !ok || count != len(slots) || req.Gender != a.Gender && !domain.CreationGenderFree(race.Family) || req.Gender < 0 || req.Gender > 1 {
		return req, domain.ErrInvalidInput
	}
	for _, slot := range slots {
		*slot = int(r.I32())
	}
	if r.Err() != nil {
		return req, r.Err()
	}
	if race.Family == 2003 && cape != domain.PhantomStarterCape {
		return req, domain.ErrInvalidInput
	}
	return req, nil
}
