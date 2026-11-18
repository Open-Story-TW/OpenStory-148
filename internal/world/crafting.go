package world

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// handleProfession serves the crafting and gathering requests. body is a
// detached copy: a request the server cannot read is dropped, not the client.
func (s *Server) handleProfession(ctx context.Context, channel int, live *liveCharacter, sink packetSink, op uint16, body []byte) error {
	r := protocol.NewReader(body)
	switch op {
	case protocol.RecvStartHarvest:
		id, err := protocol.ReadHarvest(r)
		if err != nil {
			return nil
		}
		return s.startHarvest(channel, live, sink, id)
	case protocol.RecvStopHarvest:
		id, err := protocol.ReadHarvest(r)
		if err != nil {
			return nil
		}
		return s.hitReactor(ctx, channel, live, sink, id, true)
	case protocol.RecvReactorHit:
		id, err := protocol.ReadReactorHit(r)
		if err != nil {
			return nil
		}
		if s.hitKrexelReactor(channel, live, id) {
			return nil
		}
		if handled, err := s.pirateReactorHit(channel, live, sink, id); handled {
			return err
		}
		if handled, err := s.hitAmoriaReactor(ctx, channel, live, sink, id); handled {
			return err
		}
		return s.hitReactor(ctx, channel, live, sink, id, false)
	case protocol.RecvProfessionInfo:
		q, err := protocol.ReadProfessionInfo(r)
		if err != nil || !content.ValidProfession(q.Profession) {
			return nil
		}
		live.mu.Lock()
		level := live.session.Character.Profession(q.Profession).Level
		live.mu.Unlock()
		return sink.WritePacket(protocol.ProfessionInfo(q, craftChance(q.Profession, int(q.Required)+1, level)))
	case protocol.RecvCraftEffect:
		// The effect path only decorates the crafter's own screen; the craft
		// itself is shown to the map by the make request that follows.
		return nil
	case protocol.RecvCraftMake:
		action, duration, err := protocol.ReadCraftMake(r)
		if err != nil || action != 2 && (action < 416 || action > 426) {
			return nil
		}
		return s.craftMake(channel, live, action, duration)
	case protocol.RecvCraftDone:
		recipe, err := protocol.ReadCraftDone(r)
		if err != nil {
			return nil
		}
		return s.craftDone(ctx, channel, live, sink, recipe)
	case protocol.RecvUseRecipe:
		slot, item, err := protocol.ReadUseRecipe(r)
		if err == nil {
			if err = s.useRecipe(ctx, live, sink, slot, item); err != nil {
				return err
			}
		}
		return sink.WritePacket(protocol.EnableActions())
	}
	return nil
}

// craftChance is the percent chance a recipe succeeds: certain at the
// recipe's level, 20 less per level the crafter is short. Refining what was
// gathered (herbalism and mining recipes) never fails.
func craftChance(profession, required, level int) int {
	if !content.ProfessionCraft(profession) {
		return 100
	}
	return max(0, min(100, 100-(required-level)*20))
}

func (s *Server) roomSinks(channel, mapID int) []packetSink {
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	r := g.rooms[mapKey{channel: channel, mapID: mapID}]
	if r == nil {
		return nil
	}
	out := make([]packetSink, 0, len(r.members))
	for _, m := range r.members {
		out = append(out, m.sink)
	}
	return out
}

// craftMake shows the map a character starting to craft.
func (s *Server) craftMake(channel int, live *liveCharacter, action, duration int32) error {
	live.mu.Lock()
	c := live.session.Character
	live.mu.Unlock()
	if len(c.Professions) == 0 || c.HP <= 0 {
		return nil
	}
	if duration < 0 || duration > 60000 {
		duration = 4000
	}
	packet := protocol.CraftStarted(c.ID, action, duration)
	for _, peer := range s.roomSinks(channel, c.MapID) {
		_ = peer.WritePacket(packet)
	}
	return nil
}

// craftDone settles a finished craft. The ingredients are used either way;
// a success adds one product (picked by the recipe's weights), proficiency,
// the recipe's fatigue and craft trait experience in the same commit.
func (s *Server) craftDone(ctx context.Context, channel int, live *liveCharacter, sink packetSink, recipeID int) error {
	fatigue := s.restFatigue(live, sink)
	live.mu.Lock()
	c := live.session.Character
	live.mu.Unlock()
	finish := func(result, item, quantity, exp int) error {
		packet := protocol.CraftComplete(c.ID, recipeID, result, item, quantity, exp)
		for _, peer := range s.roomSinks(channel, c.MapID) {
			if !sameDropSink(peer, sink) {
				_ = peer.WritePacket(packet)
			}
		}
		return sink.WritePacket(packet)
	}
	// Disassembly (…9000) and fusion (…9001) rebuild equipment; neither is
	// supported, and the client is told so instead of being left waiting.
	recipe, err := content.ProfessionRecipe(wzRoot(), recipeID)
	if err != nil || c.HP <= 0 {
		return finish(protocol.CraftUnknownError, 0, 0, 0)
	}
	level := c.Profession(recipe.Profession).Level
	if level == 0 || recipe.Learned && !c.KnowsRecipe(recipeID) || fatigue+recipe.Fatigue > content.ProfessionFatigueLimit {
		return finish(protocol.CraftUnknownError, 0, 0, 0)
	}
	var items []domain.ScriptItemDelta
	for _, in := range recipe.Ingredients {
		have := 0
		for _, item := range c.Items {
			if item.TemplateID == in.Item {
				have += item.Quantity
			}
		}
		// Equipment is never an ingredient the exchange can take.
		if in.Item/1000000 == 1 || have < in.Count {
			return finish(protocol.CraftUnknownError, 0, 0, 0)
		}
		items = append(items, domain.ScriptItemDelta{TemplateID: in.Item, Quantity: -in.Count})
	}
	operation := fmt.Sprintf("craft-%d-%d-%d", c.ID, recipeID, time.Now().UnixNano())
	if rand.IntN(100) >= craftChance(recipe.Profession, recipe.Level, level) {
		if _, ok, err := s.commitProfession(ctx, live, sink, operation, items, domain.ProfessionChange{}); err != nil {
			return err
		} else if !ok {
			return finish(protocol.CraftUnknownError, 0, 0, 0)
		}
		return finish(protocol.CraftFailed, 0, 0, 0)
	}
	product := pickProduct(recipe.Products)
	if !canHoldItem(c, product.Item, product.Count) {
		return finish(protocol.CraftUnknownError, 0, 0, 0)
	}
	items = append(items, domain.ScriptItemDelta{TemplateID: product.Item, Quantity: product.Count})
	change := domain.ProfessionChange{Fatigue: recipe.Fatigue, Traits: domain.Traits{Craft: 40}}
	gain := recipe.Proficiency
	if gain == 0 {
		// Recipes without their own proficiency: Legacy's rule, more for
		// tiring recipes and for recipes above the crafter's level.
		gain = (recipe.Fatigue*20 - (recipe.Level-level)*2) * 2
	}
	if gain > 0 && level < content.ProfessionMaxLevel {
		change.Gain, change.EXP = recipe.Profession, gain
	} else {
		gain = 0
	}
	if _, ok, err := s.commitProfession(ctx, live, sink, operation, items, change); err != nil {
		return err
	} else if !ok {
		return finish(protocol.CraftUnknownError, 0, 0, 0)
	}
	return finish(protocol.CraftGood, product.Item, product.Count, gain)
}

// pickProduct draws one product by probWeight.
func pickProduct(products []content.RecipeItem) content.RecipeItem {
	total := 0
	for _, p := range products {
		total += p.Weight
	}
	if total <= 0 {
		return products[0]
	}
	n := rand.IntN(total)
	for _, p := range products {
		if n < p.Weight {
			return p
		}
		n -= p.Weight
	}
	return products[0]
}

// useRecipe learns the recipe of a recipe item (251xxxx) and uses the item.
func (s *Server) useRecipe(ctx context.Context, live *liveCharacter, sink packetSink, slot, itemID int) error {
	scroll, err := content.RecipeScrollDefinition(wzRoot(), itemID)
	if err != nil {
		return nil
	}
	live.mu.Lock()
	c := live.session.Character
	live.mu.Unlock()
	held := false
	for _, item := range c.Items {
		if item.InvType == 2 && item.Slot == slot && item.TemplateID == itemID && item.Quantity > 0 {
			held = true
		}
	}
	if !held || c.HP <= 0 || c.KnowsRecipe(scroll.Recipe) || c.Profession(scroll.Recipe/10000*10000).Level < scroll.Level {
		return nil
	}
	_, _, err = s.commitProfession(ctx, live, sink, fmt.Sprintf("recipe-%d-%d-%d", c.ID, itemID, time.Now().UnixNano()), []domain.ScriptItemDelta{{TemplateID: itemID, Quantity: -1}}, domain.ProfessionChange{Recipe: scroll.Recipe})
	return err
}

// restFatigue applies the fatigue that wore off since it was last looked at,
// tells the client when it changed, and returns the current value. The stored
// value wears off the same way whenever the character is loaded.
func (s *Server) restFatigue(live *liveCharacter, sink packetSink) int {
	const step = content.ProfessionFatigueSeconds * time.Second
	now := time.Now()
	live.mu.Lock()
	c := &live.session.Character
	before := *c
	if live.fatigueAt.IsZero() || c.Fatigue <= 0 {
		live.fatigueAt = now
	} else if n := int(now.Sub(live.fatigueAt) / step); n > 0 {
		c.Fatigue = max(0, c.Fatigue-n)
		live.fatigueAt = live.fatigueAt.Add(time.Duration(n) * step)
	}
	after := *c
	live.mu.Unlock()
	if p := protocol.TraitStats(before, after); p != nil {
		_ = sink.WritePacket(p)
	}
	return after.Fatigue
}
