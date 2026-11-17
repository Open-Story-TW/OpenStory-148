package world

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

const rpsNPC = 9000019

type rpsGame struct {
	visit            *sceneVisit
	key              string
	plays            int
	wins             byte
	paid, ready, won bool
}

func (a *worldScript) openRPS(command scripting.Command) error {
	if a.npcID != rpsNPC || a.kind != "npc" || len(command.Ints) != 0 || command.Text != "" {
		return domain.ErrForbidden
	}
	a.live.persistMu.Lock()
	defer a.live.persistMu.Unlock()
	visit := a.server.sceneFor(a.live, a.sink)
	if visit == nil {
		return domain.ErrStaleOwner
	}
	if game := a.live.rps; game != nil && game.visit == visit {
		return nil
	}
	a.live.rps = &rpsGame{visit: visit, key: command.ID}
	return a.server.writeScenePackets(visit, protocol.RPSScreen(rpsNPC))
}

func (s *Server) handleRPS(ctx context.Context, live *liveCharacter, sink packetSink, payload []byte) error {
	if len(payload) == 0 || len(payload) > 2 {
		return nil
	}
	mode := payload[0]
	if mode == protocol.RPSChoose {
		if len(payload) != 2 || payload[1] > 2 {
			return nil
		}
	} else if len(payload) != 1 {
		return nil
	}

	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	game := live.rps
	visit := s.sceneFor(live, sink)
	if game == nil || visit == nil || game.visit != visit || s.tradeActive(live) {
		return nil
	}
	switch mode {
	case protocol.RPSStart, protocol.RPSRetry:
		if game.ready || game.paid && mode != protocol.RPSRetry {
			return nil
		}
		if err := s.payRPS(ctx, live, game); err != nil {
			return rpsSettlementFeedback(visit, err)
		}
		game.plays++
		err := s.rpsExchange(ctx, live, game, "entry", -1000, 0)
		if errors.Is(err, domain.ErrInsufficient) {
			live.rps = nil
			return s.writeScenePackets(visit, protocol.RPSState(protocol.RPSClose), protocol.PortalTrainingMessage("猜拳需要 1,000 楓幣。"))
		}
		if err != nil {
			game.plays--
			return rpsSettlementFeedback(visit, err)
		}
		game.paid, game.ready = true, true
		return s.writeScenePackets(visit, protocol.RPSState(protocol.RPSReady))
	case protocol.RPSChoose:
		if !game.ready {
			return nil
		}
		value, err := rand.Int(rand.Reader, big.NewInt(3))
		if err != nil {
			return err
		}
		return s.writeScenePackets(visit, game.choose(payload[1], byte(value.Int64())))
	case protocol.RPSTimeout:
		if !game.ready {
			return nil
		}
		game.ready = false
		return s.writeScenePackets(visit, protocol.RPSState(protocol.RPSExpired))
	case protocol.RPSNext:
		if !game.won {
			return nil
		}
		if game.wins < 10 {
			game.ready, game.won = true, false
			return s.writeScenePackets(visit, protocol.RPSState(protocol.RPSNewRound))
		}
		fallthrough
	case protocol.RPSLeave:
		if err := s.payRPS(ctx, live, game); err != nil {
			return rpsSettlementFeedback(visit, err)
		}
		live.rps = nil
		return s.writeScenePackets(visit, protocol.RPSState(protocol.RPSClose))
	}
	return nil
}

func rpsSettlementFeedback(visit *sceneVisit, err error) error {
	slog.Warn("rps_settlement_failed", "error", err)
	return visit.sink.WritePacket(protocol.PortalTrainingMessage("猜拳結算暫時無法完成，請確認背包有空位後再試。"))
}

func (g *rpsGame) choose(hand, response byte) []byte {
	result := g.wins
	if hand != response {
		g.ready = false
		if (hand+2)%3 == response {
			g.won = true
			g.wins++
			result = g.wins
		} else {
			result = 255
		}
	}
	return protocol.RPSResult(response, result)
}

func (s *Server) payRPS(ctx context.Context, live *liveCharacter, game *rpsGame) error {
	if !game.paid {
		return nil
	}
	var err error
	if game.won {
		err = s.rpsExchange(ctx, live, game, "prize", 0, 4031331+int(game.wins))
	} else if game.wins == 0 {
		err = s.rpsExchange(ctx, live, game, "refund", 500, 0)
	}
	if err == nil {
		game.wins, game.ready, game.won, game.paid = 0, false, false, false
	}
	return err
}

func (s *Server) rpsExchange(ctx context.Context, live *liveCharacter, game *rpsGame, action string, mesos int64, item int) error {
	repo, ok := s.repo.(domain.ScriptRepository)
	if !ok {
		return domain.ErrForbidden
	}
	req := domain.ScriptExchange{Session: s.skillSession(live), OperationID: fmt.Sprintf("%s-%d-%s", game.key, game.plays, action), MesosDelta: mesos}
	if item != 0 {
		req.Items = []domain.ScriptItemDelta{{TemplateID: item, Quantity: 1}}
	}
	exchangeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	value, err := repo.ExecuteScriptExchange(exchangeCtx, req)
	if err != nil {
		return err
	}
	s.applyInventoryResult(live, value.Character)
	packets := [][]byte{protocol.UpdateMesos(value.Character.Mesos, true)}
	if len(value.Changes) > 0 {
		packets = append(packets, protocol.ModifyInventory(value.Changes))
	}
	return s.writeScenePackets(game.visit, packets...)
}
