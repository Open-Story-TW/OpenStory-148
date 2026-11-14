package content

import (
	"errors"
	"fmt"

	"maple-go-server/internal/domain"
)

const PotentialPolicyVersion = "java-legacy-local-special-fallback-v1"

var (
	ErrPotentialInput      = errors.New("unsupported or invalid potential policy input")
	ErrPotentialIneligible = errors.New("potential operation is not eligible")
	ErrPotentialCandidates = errors.New("potential operation has no usable candidates")
	ErrPotentialDrawLimit  = errors.New("potential RNG draw limit reached")
	ErrPotentialRNG        = errors.New("potential RNG failed or returned an out-of-bound value")
)

// PotentialRandN must return an independent uniform integer in [0, bound).
// The transaction owner injects it only after locking/replay checks. This pure
// engine never retries an operation or chooses a seed compatible with Java.
type PotentialRandN func(bound int) (int, error)

type PotentialPolicy struct {
	GradeBound           int  `json:"grade_bound"`
	SpecialKeepThreshold int  `json:"special_keep_threshold"`
	MysteryLevelClamp    bool `json:"mystery_level_clamp"`
	MaxDraws             int  `json:"max_draws"`
}

func DefaultPotentialPolicy() PotentialPolicy {
	return PotentialPolicy{GradeBound: 1000, SpecialKeepThreshold: 10, MysteryLevelClamp: true, MaxDraws: 4096}
}

// PotentialTarget contains authoritative equipment/WZ facts supplied by the
// caller, never values copied from the client's action. Inventory ownership,
// session, resources, expiry and transaction receipts remain the caller's job.
// ItemFlags is the equipment's general item flags, NOT Potential.Flags or a WZ
// option's flags. OptionLevelOverrides contains authoritative WZ info/option
// levels in native row order. The adapter must zero entries beyond the source
// row count; unknown inline memory beyond that count is not an override.
type PotentialTarget struct {
	ItemID               int                       `json:"item_id"`
	RequiredLevel        int                       `json:"required_level"`
	ItemFlags            uint16                    `json:"item_flags"`
	OptionLevelOverrides [5]int                    `json:"option_level_overrides"`
	Cash                 bool                      `json:"cash"`
	UpgradeSlots         int                       `json:"upgrade_slots"`
	ScrollLevel          int                       `json:"scroll_level"`
	Potential            domain.EquipmentPotential `json:"potential"`
}

type PotentialSealRequest struct {
	Target   PotentialTarget `json:"target"`
	ScrollID int             `json:"scroll_id"`
	Vegas    int             `json:"vegas"`
}

type PotentialMagnifyRequest struct {
	Target      PotentialTarget `json:"target"`
	MagnifierID int             `json:"magnifier_id"`
}

type PotentialDraw struct {
	Purpose           string `json:"purpose"`
	Bound             int    `json:"bound"`
	Value             int    `json:"value"`
	Pass              int    `json:"pass,omitempty"`
	Line              int    `json:"line,omitempty"` // one-based; zero for rank/line-count draws
	SourceOptionID    int    `json:"source_option_id,omitempty"`
	EffectiveOptionID int    `json:"effective_option_id,omitempty"`
}

type PotentialRolledOption struct {
	Line          int            `json:"line"`
	SourceID      int            `json:"source_id"`
	EffectiveID   int            `json:"effective_id"`
	CandidateBand int            `json:"candidate_band"` // explicit WZ band, one-based
	StatBand      int            `json:"stat_band"`      // native per-line band including flags/overrides
	Retained      bool           `json:"retained"`
	Stats         PotentialLevel `json:"stats,omitzero"` // retained-line source data, not runtime stat application
}

// PotentialPlan is a detached proposal, not an applied inventory result. Store
// the plan/transcript under the operation receipt and replay that result rather
// than calling the RNG again. Any error returns a zero plan: no partial plan is
// safe to spend resources or publish. Flags and all five source words survive
// unless the actual Java operation changes their corresponding potential.
type PotentialPlan struct {
	PolicyVersion string                    `json:"policy_version"`
	WZSHA256      string                    `json:"wz_sha256"`
	Policy        PotentialPolicy           `json:"policy"`
	Operation     string                    `json:"operation"`
	Target        PotentialTarget           `json:"target"`
	ToolID        int                       `json:"tool_id"`
	Before        domain.EquipmentPotential `json:"before"`
	After         domain.EquipmentPotential `json:"after"`
	Tier          int                       `json:"tier"`
	RollLines     int                       `json:"roll_lines"`
	ScrollSuccess bool                      `json:"scroll_success"`
	ToolCount     int                       `json:"tool_count"`
	MesoFee       int                       `json:"meso_fee"`
	Draws         []PotentialDraw           `json:"draws"`
	Options       []PotentialRolledOption   `json:"options,omitempty"`
}

func validatePotentialPolicy(c *PotentialCatalog, p PotentialPolicy, randN PotentialRandN) error {
	if c == nil || len(c.ids) == 0 || c.sha256 == "" || randN == nil || p.GradeBound < 1 || p.GradeBound > 1<<30 ||
		p.SpecialKeepThreshold < -1 || p.SpecialKeepThreshold > 100 || p.MaxDraws < 1 || p.MaxDraws > 65536 {
		return ErrPotentialInput
	}
	return nil
}

func validatePotentialTarget(t PotentialTarget) error {
	if t.ItemID < 1000000 || t.ItemID >= 2000000 || t.RequiredLevel < 0 || t.RequiredLevel > 65535 ||
		t.UpgradeSlots < 0 || t.UpgradeSlots > 255 || t.ScrollLevel < 0 || t.ScrollLevel > 255 {
		return ErrPotentialInput
	}
	for _, level := range t.OptionLevelOverrides {
		if level < -1<<31 || level > 1<<31-1 {
			return ErrPotentialInput
		}
	}
	return nil
}

// decodePotential interprets only the Java-compatible policy subset. The domain
// intentionally preserves every native bit pattern, but an opaque imported
// payload is not automatically authority for a roll. Negative markers encoded
// as U16 are distinct from revealed IDs 40000..49999, which stay unsigned.
func decodePotential(p domain.EquipmentPotential) ([5]int, int, error) {
	var values [5]int
	for i, word := range p.Options {
		values[i] = int(word)
		if p.RawState == 1 || word >= 60000 {
			values[i] = int(int16(word))
		}
	}
	state := javaPotentialState(values)
	if state != int(p.RawState) {
		return [5]int{}, 0, ErrPotentialInput
	}
	switch state {
	case 0, 1, 17, 18, 19, 20:
		return values, state, nil
	default:
		return [5]int{}, 0, ErrPotentialInput
	}
}

func javaPotentialState(values [5]int) int {
	sum := 0
	state := 0
	for _, v := range values {
		sum += v
		switch {
		case v >= 40000:
			state = 20
		case v >= 30000 && state < 19:
			state = 19
		case v >= 20000 && state < 18:
			state = 18
		}
	}
	if state != 0 {
		return state
	}
	if sum > 0 {
		return 17
	}
	if sum < 0 {
		return 1
	}
	return 0
}

func encodePotential(before domain.EquipmentPotential, values [5]int) domain.EquipmentPotential {
	after := before
	after.Present = true
	after.RawState = uint8(javaPotentialState(values))
	for i, v := range values {
		after.Options[i] = uint16(v)
	}
	return after
}

type potentialRoller struct {
	p     PotentialPolicy
	randN PotentialRandN
	draws []PotentialDraw
}

func (r *potentialRoller) draw(purpose string, bound, pass, line, source int) (int, error) {
	if len(r.draws) >= r.p.MaxDraws {
		return 0, ErrPotentialDrawLimit
	}
	n, err := r.randN(bound)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrPotentialRNG, err)
	}
	if n < 0 || n >= bound {
		return 0, ErrPotentialRNG
	}
	r.draws = append(r.draws, PotentialDraw{Purpose: purpose, Bound: bound, Value: n, Pass: pass, Line: line, SourceOptionID: source})
	return n, nil
}

func potentialLineCount(values [5]int) int {
	lines := 2
	for _, v := range values[1:4] { // Java does not count potential5.
		if v != 0 {
			lines++
		}
	}
	return lines
}

// SealPotential follows the actual potential-scroll branch, including the
// broken-but-reachable epic state0/1 cases. There is no WZ success/curse trial,
// no guaranteed epic tier and no upgrade-slot decrement in this branch.
func SealPotential(c *PotentialCatalog, req PotentialSealRequest, p PotentialPolicy, randN PotentialRandN) (PotentialPlan, error) {
	if err := validatePotentialPolicy(c, p, randN); err != nil {
		return PotentialPlan{}, err
	}
	if err := validatePotentialTarget(req.Target); err != nil {
		return PotentialPlan{}, err
	}
	values, state, err := decodePotential(req.Target.Potential)
	if err != nil {
		return PotentialPlan{}, err
	}
	epic := req.ScrollID/100 == 20497
	if req.ScrollID/100 != 20494 && !epic && req.ScrollID != 5534000 {
		return PotentialPlan{}, ErrPotentialIneligible
	}
	if req.Target.Cash || req.Vegas != 0 || req.Target.ItemID/100000 == 19 || (!epic && state >= 1) || (epic && state >= 18) ||
		(!epic && req.Target.ScrollLevel == 0 && req.Target.UpgradeSlots == 0 && req.Target.ItemID/10000 != 135) {
		return PotentialPlan{}, ErrPotentialIneligible
	}
	r := potentialRoller{p: p, randN: randN}
	if epic {
		rate := 6
		if state == 17 {
			rate = 20
		}
		grade, err := r.draw("epic_grade", p.GradeBound, 0, 0, 0)
		if err != nil {
			return PotentialPlan{}, err
		}
		rank := -state
		if grade < rate && state != 20 {
			rank--
		}
		old := values
		values[0] = rank
		if old[2] > 0 {
			values[1] = rank
		} else {
			n, err := r.draw("epic_third_line", 10, 0, 0, 0)
			if err != nil {
				return PotentialPlan{}, err
			}
			values[1] = 0
			if n <= 1 {
				values[1] = rank
			}
		}
		values[2], values[3], values[4] = 0, 0, 0
		if old[3] > 0 {
			values[2] = rank
		}
		if old[4] > 0 {
			values[3] = rank
		}
	} else {
		rank := -17
		n, err := r.draw("initial_epic", 100, 0, 0, 0)
		if err != nil {
			return PotentialPlan{}, err
		}
		if n < 20 {
			rank = -18
			n, err = r.draw("initial_unique", 100, 0, 0, 0)
			if err != nil {
				return PotentialPlan{}, err
			}
			if n < 12 {
				rank = -19
				n, err = r.draw("initial_legendary", 100, 0, 0, 0)
				if err != nil {
					return PotentialPlan{}, err
				}
				if n < 8 {
					rank = -20
				}
			}
		}
		values = [5]int{rank}
		n, err = r.draw("initial_third_line", 10, 0, 0, 0)
		if err != nil {
			return PotentialPlan{}, err
		}
		if n == 0 {
			values[1] = rank
		}
	}
	after := encodePotential(req.Target.Potential, values)
	tier := -values[0]
	return PotentialPlan{PolicyVersion: PotentialPolicyVersion, WZSHA256: c.sha256, Policy: p, Operation: "seal", Target: req.Target, ToolID: req.ScrollID, Before: req.Target.Potential,
		After: after, Tier: tier, RollLines: potentialLineCount(values), ScrollSuccess: (epic && after.RawState == 1) || int(after.RawState) > state,
		ToolCount: 1, Draws: r.draws}, nil
}

// PotentialStatBand follows native581B67/5559FA for one zero-based option index.
// Apply the default index, then the item-flag minimum, then a positive WZ level
// override, and only then the unsigned index>=21 fallback. Returning explicit
// WZ band21 is possible; the caller must fail if that band is absent in its WZ.
func PotentialStatBand(target PotentialTarget, line int) (int, error) {
	if target.RequiredLevel < 0 || target.RequiredLevel > 65535 || line < 0 || line >= 5 {
		return 0, ErrPotentialInput
	}
	for _, level := range target.OptionLevelOverrides {
		if level < -1<<31 || level > 1<<31-1 {
			return 0, ErrPotentialInput
		}
	}
	index := 0
	if target.RequiredLevel > 0 {
		index = (target.RequiredLevel - 1) / 10
	}
	minimum := 0
	if target.ItemFlags&0x800 != 0 {
		minimum = 12
	} else if target.ItemFlags&0x400 != 0 {
		minimum = 11
	}
	if index < minimum {
		index = minimum
	}
	if override := target.OptionLevelOverrides[line]; override > 0 {
		index = override - 1
	}
	if uint32(index) >= 21 {
		index = 0
	}
	return index + 1, nil
}

// PotentialOrdinaryNoOverrideStatBand is only the default branch, for callers
// which have established that neither general item-flag minimum nor per-option
// WZ override applies. It must not replace PotentialStatBand on real equipment.
func PotentialOrdinaryNoOverrideStatBand(requiredLevel int) (int, error) {
	return PotentialStatBand(PotentialTarget{RequiredLevel: requiredLevel}, 0)
}

func PotentialMagnifyFee(requiredLevel, tier int) (int, error) {
	if requiredLevel < 0 || requiredLevel > 65535 || tier < 17 || tier > 20 {
		return 0, ErrPotentialInput
	}
	fees := [4]int{50000, 200000, 420000, 550000}
	switch {
	case requiredLevel <= 100:
		fees = [4]int{1000, 5000, 10000, 15000}
	case requiredLevel <= 120:
		fees = [4]int{5000, 10000, 30000, 50000}
	case requiredLevel <= 130:
		fees = [4]int{50000, 100000, 150000, 200000}
	}
	return fees[tier-17], nil
}

func potentialGenerationBand(requiredLevel int, p PotentialPolicy) int {
	index := requiredLevel / 10
	if p.MysteryLevelClamp && index >= 20 {
		index = 19
	}
	return index + 1
}

func potentialIDFits(id, tier, line int) bool {
	if id < 10000 || id >= 60000 || tier < 17 || tier > 20 || line < 0 || line >= 5 {
		return false
	}
	lower := (tier - 16) * 10000
	if line > 0 {
		return id >= lower-10000 && id <= lower+9999
	}
	return id >= lower && id <= lower+9999
}

func potentialAccessory(item int) bool {
	if (item >= 1010000 && item < 1040000) || (item >= 1122000 && item < 1153000) || (item >= 1112000 && item < 1113000) ||
		(item >= 1113212 && item < 1113217) {
		return true
	}
	switch item {
	case 1113075, 1113074, 1113195, 1113196, 1113211, 1113231, 1113132, 1113185, 1113072, 1113087, 1113088,
		1113085, 1113086, 1113020, 1112952, 1112951, 1113089, 1112794, 1112478, 1112713, 1113039:
		return true
	}
	return false
}

func PotentialOptionTypeFits(optionType, item int) bool {
	weapon := item >= 1300000 && item < 1600000
	switch optionType {
	case 10:
		return weapon || item/100000 == 16 || (item >= 1099000 && item <= 1099015)
	case 11:
		return !weapon
	case 20:
		return !potentialAccessory(item) && !weapon
	case 40:
		return potentialAccessory(item)
	case 51:
		return item/10000 == 100
	case 52:
		return item/10000 == 104 || item/10000 == 105
	case 53:
		return item/10000 == 106 || item/10000 == 105
	case 54:
		return item/10000 == 108
	case 55:
		return item/10000 == 107
	default:
		return true
	}
}

func eligiblePotential(c *PotentialCatalog, id int, t PotentialTarget, tier, line, band int) bool {
	o := c.options[id]
	_, hasBand := o.Levels[band]
	return hasBand && o.RequiredLevel/10 <= band-1 && PotentialOptionTypeFits(o.OptionType, t.ItemID) && potentialIDFits(id, tier, line)
}

// EligiblePotentialOptions returns sorted source IDs, one entry per eligible
// option row regardless of its WZ weight. This query does not draw RNG or expose
// shared maps. Line is zero-based, matching Java potentialIDFits.
func EligiblePotentialOptions(c *PotentialCatalog, t PotentialTarget, tier, line int, p PotentialPolicy) ([]int, error) {
	if c == nil || len(c.ids) == 0 || tier < 17 || tier > 20 || line < 0 || line > 4 {
		return nil, ErrPotentialInput
	}
	if err := validatePotentialTarget(t); err != nil {
		return nil, err
	}
	band := potentialGenerationBand(t.RequiredLevel, p)
	out := make([]int, 0)
	for _, id := range c.ids {
		if eligiblePotential(c, id, t, tier, line, band) {
			out = append(out, id)
		}
	}
	return out, nil
}

// MagnifyPotential preserves Java's uniform rejection sampling and outer rank
// retry, including discarded fourth/fifth draws. The one intentional RNG-policy
// correction is local40650/40656 fallback: it changes only this draw's result,
// never the shared catalog or later characters' probabilities.
func MagnifyPotential(c *PotentialCatalog, req PotentialMagnifyRequest, p PotentialPolicy, randN PotentialRandN) (PotentialPlan, error) {
	if err := validatePotentialPolicy(c, p, randN); err != nil {
		return PotentialPlan{}, err
	}
	if err := validatePotentialTarget(req.Target); err != nil {
		return PotentialPlan{}, err
	}
	values, state, err := decodePotential(req.Target.Potential)
	if err != nil {
		return PotentialPlan{}, err
	}
	levelIndex := req.Target.RequiredLevel / 10
	if state != 1 || req.MagnifierID < 2460000 || req.MagnifierID > 2460003 ||
		(req.MagnifierID == 2460000 && levelIndex > 3) || (req.MagnifierID == 2460001 && levelIndex > 7) || (req.MagnifierID == 2460002 && levelIndex > 12) {
		return PotentialPlan{}, ErrPotentialIneligible
	}
	tier := values[0]
	if tier < 0 {
		tier = -tier
	}
	if tier < 17 || tier > 20 {
		tier = 17
	}
	lines := potentialLineCount(values)
	band := potentialGenerationBand(req.Target.RequiredLevel, p)
	var statBands [5]int
	for line := 0; line < lines; line++ {
		statBands[line], _ = PotentialStatBand(req.Target, line)
	}
	// Complete catalog/index validation happens before the first RNG call. The
	// real WZ has twenty levels; never charge and then reproduce Java's get(20)
	// exception, or spin forever when no candidate can match.
	for _, id := range c.ids {
		if _, ok := c.options[id].Levels[band]; !ok {
			return PotentialPlan{}, ErrPotentialCandidates
		}
	}
	for line := 0; line < lines; line++ {
		ids, err := EligiblePotentialOptions(c, req.Target, tier, line, p)
		if err != nil || len(ids) == 0 {
			return PotentialPlan{}, ErrPotentialCandidates
		}
		if line >= 3 {
			// Java still draws these lines and then writes zero. They have no
			// applied native option, so an unused stat band must not reject the
			// roll or manufacture stats for a discarded option.
			continue
		}
		for _, id := range ids {
			if _, ok := c.options[id].Levels[statBands[line]]; !ok {
				return PotentialPlan{}, ErrPotentialCandidates
			}
			if (id == 40650 || id == 40656) && p.SpecialKeepThreshold < 99 {
				fallback, ok := c.options[10001]
				if _, hasBand := fallback.Levels[statBands[line]]; !ok || !hasBand {
					return PotentialPlan{}, ErrPotentialCandidates
				}
			}
		}
	}
	r := potentialRoller{p: p, randN: randN}
	var rolled []PotentialRolledOption
	for pass := 1; javaPotentialState(values) != tier; pass++ {
		rolled = nil
		for line := 0; line < lines; line++ {
			for {
				n, err := r.draw("option_row", len(c.ids), pass, line+1, 0)
				if err != nil {
					return PotentialPlan{}, err
				}
				sourceID := c.ids[n]
				r.draws[len(r.draws)-1].SourceOptionID = sourceID
				if !eligiblePotential(c, sourceID, req.Target, tier, line, band) {
					continue
				}
				effectiveID := sourceID
				if sourceID == 40650 || sourceID == 40656 {
					n, err = r.draw("special_keep", 100, pass, line+1, sourceID)
					if err != nil {
						return PotentialPlan{}, err
					}
					if n > p.SpecialKeepThreshold {
						effectiveID = 10001
					}
				}
				r.draws[len(r.draws)-1].EffectiveOptionID = effectiveID
				retained := line < 3
				var stats PotentialLevel
				if retained {
					stats = clonePotentialLevel(c.options[effectiveID].Levels[statBands[line]])
				}
				rolled = append(rolled, PotentialRolledOption{Line: line + 1, SourceID: sourceID, EffectiveID: effectiveID,
					CandidateBand: band, StatBand: statBands[line], Retained: retained, Stats: stats})
				values[line] = 0
				if retained {
					values[line] = effectiveID
				}
				break
			}
		}
	}
	fee, _ := PotentialMagnifyFee(req.Target.RequiredLevel, tier)
	return PotentialPlan{PolicyVersion: PotentialPolicyVersion, WZSHA256: c.sha256, Policy: p, Operation: "magnify", Target: req.Target, ToolID: req.MagnifierID, Before: req.Target.Potential,
		After: encodePotential(req.Target.Potential, values), Tier: tier, RollLines: lines, ScrollSuccess: true, ToolCount: 1, MesoFee: fee, Draws: r.draws, Options: rolled}, nil
}
