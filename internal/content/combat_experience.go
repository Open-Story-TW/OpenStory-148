package content

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"

	"maple-go-server/internal/domain"
)

type CombatExperienceRules struct {
	MobID, MobLevel       int
	MaximumHP, Experience int64
	PartyBonusRate        int
}

// CombatExperienceMetadata uses the actual authority HP/EXP from Catalog and
// the Legacy party-bonus WZ facts. Custom rate buffs remain separate policies.
func CombatExperienceMetadata(root string, mapID, mobID int) (CombatExperienceRules, error) {
	c, err := DefaultCatalog()
	if err != nil {
		return CombatExperienceRules{}, err
	}
	mob, err := c.Mob(mobID)
	if err != nil {
		return CombatExperienceRules{}, err
	}
	if JudgementRoom(mapID) {
		for _, wave := range judgementWaves[mapID-JudgementRoomFirst] {
			if wave.TemplateID == mobID {
				mob = wave.Stats(mob)
				return CombatExperienceRules{MobID: mobID, MobLevel: mob.Level, MaximumHP: mob.HP, Experience: mob.EXP, PartyBonusRate: 5}, nil
			}
		}
		return CombatExperienceRules{}, domain.ErrInvalidInput
	}
	r := CombatExperienceRules{MobID: mobID, MobLevel: mob.Level, MaximumHP: mob.HP, Experience: mob.EXP, PartyBonusRate: 5}
	if root == "" {
		return r, nil
	}
	n, err := readWZ(filepath.Join(root, "Mob.wz", fmt.Sprintf("%07d.img.xml", mobID)))
	if err != nil {
		return r, err
	}
	info := n.child("info")
	if link := info.intValue("link", 0); link > 0 && link != mobID {
		n, err = readWZ(filepath.Join(root, "Mob.wz", fmt.Sprintf("%07d.img.xml", link)))
		if err != nil {
			return r, err
		}
		info = n.child("info")
	}
	if rate := info.intValue("partyBonusR", 0); rate > 0 {
		r.PartyBonusRate = rate
		return r, nil
	}
	partyMob := info.intValue("partyBonusMob", 0) > 0
	for _, id := range []int{4250000, 4250001, 5250000, 5250001, 5250002, 9400661, 9400660, 9400659, 9400658} {
		partyMob = partyMob || mobID == id
	}
	if !partyMob {
		return r, nil
	}
	mapRate := 0
	switch mapID {
	case 300010000, 300010100, 300010200, 300010300, 300010400, 300020000, 300020100, 300020200, 300030000, 683070400, 683070401, 683070402:
		mapRate = 25
	default:
		n, err = readWZ(filepath.Join(root, "Map.wz", "Map", fmt.Sprintf("Map%d", mapID/100000000), fmt.Sprintf("%09d.img.xml", mapID)))
		if err != nil {
			return r, err
		}
		mapRate = n.child("info").intValue("partyBonusR", 0) / 2 // GameConstants.getPartyPlay(map,def).
	}
	if mapRate > 0 {
		r.PartyBonusRate = mapRate
	}
	return r, nil
}

func CombatTargetExperienceMetadata(root string, mapID int, target domain.CombatTarget) (CombatExperienceRules, error) {
	if target.MonsterRush && (target.MobID != MonsterRushMob || !MonsterRushMap(mapID)) {
		return CombatExperienceRules{}, domain.ErrInvalidInput
	}
	rules, err := CombatExperienceMetadata(root, mapID, target.MobID)
	if err == nil && target.MonsterRush {
		rules.MaximumHP = MonsterRushHP
		rules.Experience = monsterRushExperience
	}
	return rules, err
}

// CombatExperienceGrants preserves Java's group ceil, per-contributor 20/80
// round, cumulative base, and LAST contributor metadata. Sorting contributors
// by character identity makes the HashMap-dependent overwrite deterministic.
// Receivers must be live in the current room; absent contribution damage stays
// in denominators and is never reassigned to the final hitter.
func CombatExperienceGrants(r CombatExperienceRules, world, channel, mapID int, damage []domain.CombatContribution, members []domain.CharacterSession, parties []domain.CombatPartyPin) ([]domain.CombatGrant, error) {
	if r.MaximumHP < 1 || r.Experience < 0 || r.Experience > domain.MaxExperience() || r.PartyBonusRate < 0 || r.PartyBonusRate > 10000 {
		return nil, domain.ErrInvalidInput
	}
	live := map[int64]domain.CharacterSession{}
	for _, s := range members {
		if s.CharacterID <= 0 || live[s.CharacterID].CharacterID != 0 {
			return nil, domain.ErrInvalidInput
		}
		live[s.CharacterID] = s
	}
	partyByID := map[int64]int64{}
	partyMembers := map[int64][]int64{}
	for _, p := range parties {
		if p.ID <= 0 || len(p.Members) < 1 || len(p.Members) > domain.PartyCapacity {
			return nil, domain.ErrInvalidInput
		}
		partyMembers[p.ID] = p.Members
		for _, id := range p.Members {
			if partyByID[id] != 0 {
				return nil, domain.ErrInvalidInput
			}
			partyByID[id] = p.ID
		}
	}
	ordered := append([]domain.CombatContribution(nil), damage...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.CharacterID != b.CharacterID {
			return a.CharacterID < b.CharacterID
		}
		if a.Generation != b.Generation {
			return a.Generation < b.Generation
		}
		return a.SessionID < b.SessionID
	})
	type group struct{ party, id int64 }
	groupOf := func(id int64) group {
		if p := partyByID[id]; p > 0 {
			return group{party: p}
		}
		return group{id: id}
	}
	groups := map[group]int64{}
	var total int64
	for i, d := range ordered {
		if d.CharacterID <= 0 || d.Damage <= 0 || d.Damage > r.MaximumHP || d.Generation < 1 || d.SessionID == "" || i > 0 && d.CharacterID == ordered[i-1].CharacterID && d.Generation == ordered[i-1].Generation && d.SessionID == ordered[i-1].SessionID {
			return nil, domain.ErrInvalidInput
		}
		if d.Damage > r.MaximumHP-total {
			return nil, domain.ErrInvalidInput
		}
		total += d.Damage
		groups[groupOf(d.CharacterID)] += d.Damage
	}
	grants := map[int64]domain.CombatGrant{}
	eligible := func(s domain.CharacterSession) bool {
		return s.Lease.WorldID == world && s.ChannelID == channel && s.Character.MapID == mapID && s.Character.HP > 0
	}
	for _, d := range ordered {
		source, ok := live[d.CharacterID]
		if !ok || source.SessionID != d.SessionID || source.Generation != d.Generation || source.Lease.WorldID != world || source.ChannelID != channel || source.Character.MapID != mapID {
			continue
		}
		g := groupOf(d.CharacterID)
		groupBase := int64(math.Ceil(float64(r.Experience) * float64(groups[g]) / float64(r.MaximumHP)))
		if g.party == 0 {
			if eligible(source) {
				grant := grants[d.CharacterID]
				grant.CharacterID = d.CharacterID
				grant.BaseEXP += int64(math.Ceil(float64(r.Experience) * float64(d.Damage) / float64(r.MaximumHP)))
				grant.QuestKill = true
				grants[d.CharacterID] = grant
			}
			continue
		}
		var receivers []domain.CharacterSession
		sumLevels := 0
		for _, id := range partyMembers[g.party] {
			receiver, ok := live[id]
			if ok && eligible(receiver) && (source.Character.Level-receiver.Character.Level <= 5 || r.MobLevel-receiver.Character.Level <= 5) {
				receivers = append(receivers, receiver)
				sumLevels += receiver.Character.Level
			}
		}
		if sumLevels <= 0 {
			continue
		}
		inner := float64(groupBase) * float64(d.Damage) / float64(groups[g])
		for _, receiver := range receivers {
			fraction := 0.8 * float64(receiver.Character.Level) / float64(sumLevels)
			if receiver.CharacterID == d.CharacterID {
				fraction += 0.2
			}
			grant := grants[receiver.CharacterID]
			grant.CharacterID = receiver.CharacterID
			grant.BaseEXP += int64(math.Floor(fraction*inner + 0.5))
			grant.PartySize = len(receivers)
			grant.QuestKill = true
			grants[receiver.CharacterID] = grant
		}
	}
	out := make([]domain.CombatGrant, 0, len(grants))
	for _, g := range grants {
		if g.PartySize > 1 {
			adjust := 1
			if r.PartyBonusRate > 5 {
				adjust = -1
			}
			g.PartyEXP = int64(float32(float64(g.BaseEXP)*float64(r.PartyBonusRate)/100) * float32(g.PartySize+adjust))
		}
		g.Experience = domain.AddExperience(min(g.BaseEXP, domain.MaxExperience()), g.PartyEXP)
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CharacterID < out[j].CharacterID })
	return out, nil
}
