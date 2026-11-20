package content

import (
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"maple-go-server/internal/domain"
)

// PotentialEffectSupport describes a source attribute, not a claim that every
// corresponding gameplay consumer exists. Event effects retain their complete
// source line and explicitly block the checked combat provider until supported.
type PotentialEffectSupport string

const (
	PotentialNumeric  PotentialEffectSupport = "numeric"
	PotentialSelector PotentialEffectSupport = "source_selector"
	PotentialPending  PotentialEffectSupport = "pending_runtime_effect"
	PotentialViewOnly PotentialEffectSupport = "effective_view_only"
)

var potentialEffectSupport = map[string]PotentialEffectSupport{
	"incSTR": PotentialNumeric, "incDEX": PotentialNumeric, "incINT": PotentialNumeric, "incLUK": PotentialNumeric,
	"incSTRr": PotentialNumeric, "incDEXr": PotentialNumeric, "incINTr": PotentialNumeric, "incLUKr": PotentialNumeric,
	"incMHP": PotentialNumeric, "incMMP": PotentialNumeric, "incMHPr": PotentialNumeric, "incMMPr": PotentialNumeric,
	"incPAD": PotentialNumeric, "incMAD": PotentialNumeric, "incPADr": PotentialNumeric, "incMADr": PotentialNumeric,
	"incACC": PotentialNumeric, "incACCr": PotentialNumeric, "incCr": PotentialNumeric,
	"incCriticaldamageMax": PotentialNumeric, "ignoreTargetDEF": PotentialNumeric, "incDAMr": PotentialNumeric,
	"incPDD": PotentialViewOnly, "incMDD": PotentialViewOnly, "incPDDr": PotentialViewOnly, "incMDDr": PotentialViewOnly,
	"incSpeed": PotentialViewOnly, "incJump": PotentialViewOnly, "incEVAr": PotentialViewOnly,
	"incCriticaldamageMin": PotentialViewOnly, "incAsrR": PotentialViewOnly, "incTerR": PotentialViewOnly,
	// Native row11 contributes flat EVA; full native miss/base/RNG acceptance
	// remains separate from this checked effective-stat view.
	"incEVA":     PotentialViewOnly,
	"RecoveryHP": PotentialNumeric, "RecoveryMP": PotentialNumeric, "RecoveryUP": PotentialNumeric,
	"HP": PotentialPending, "MP": PotentialPending, "ignoreDAM": PotentialPending, "ignoreDAMr": PotentialPending,
	"DAMreflect": PotentialPending, "incAllskill": PotentialPending, "mpconReduce": PotentialNumeric,
	"reduceCooltime": PotentialNumeric, "incMesoProp": PotentialPending, "incRewardProp": PotentialPending,
	"attackType": PotentialPending, "level": PotentialPending, "prop": PotentialPending,
	// Some original rows (e.g.20366 invulnerability duration and20396
	// abnormal-duration reduction) contain only time. Selector-looking source
	// data cannot silently become an implemented no-op without its event hook.
	"time": PotentialPending, "face": PotentialPending, "boss": PotentialSelector,
}

// PotentialAttributeSupport returns detached coverage for all known original
// source attributes. An unknown future attribute is an error, never a zero.
func PotentialAttributeSupport() map[string]PotentialEffectSupport {
	out := make(map[string]PotentialEffectSupport, len(potentialEffectSupport))
	for k, v := range potentialEffectSupport {
		out[k] = v
	}
	return out
}

type PotentialEffectLine struct {
	EquipmentID int64          `json:"equipment_id"`
	Slot        int            `json:"slot"`
	Line        int            `json:"line"`
	OptionID    int            `json:"option_id"`
	Band        int            `json:"band"`
	Attributes  PotentialLevel `json:"attributes"`
}

type PendingPotentialEffect struct {
	EquipmentID int64    `json:"equipment_id"`
	Line        int      `json:"line"`
	OptionID    int      `json:"option_id"`
	Attributes  []string `json:"attributes"`
}

// PotentialEffects is a detached numeric foundation plus explicit unresolved
// events. Values are additive source fields; damage multipliers remain ordered
// per-line factors, because Java multiplies each option rather than adding %.
type PotentialEffects struct {
	SourceSHA256        string                         `json:"source_sha256,omitempty"`
	Values              map[string]int                 `json:"values"`
	DamagePercents      []int                          `json:"damage_percents,omitempty"`
	BossDamagePercents  []int                          `json:"boss_damage_percents,omitempty"`
	Lines               []PotentialEffectLine          `json:"lines,omitempty"`
	Pending             []PendingPotentialEffect       `json:"pending,omitempty"`
	NativeContactDamage []NativeContactDamagePotential `json:"native_contact_damage,omitempty"`
	AttackProcs         []AttackProcPotential          `json:"attack_procs,omitempty"`
}

type PendingPotentialEffectsError struct{ Effects []PendingPotentialEffect }

func (e *PendingPotentialEffectsError) Error() string {
	return fmt.Sprintf("%d potential lines require unimplemented runtime effects", len(e.Effects))
}

type potentialEffectsCatalog struct {
	once    sync.Once
	catalog *PotentialCatalog
	err     error
}

var potentialEffectsCatalogs sync.Map

func cachedPotentialEffectsCatalog(root string) (*PotentialCatalog, error) {
	if root == "" {
		return nil, fmt.Errorf("revealed potential requires explicit original WZ")
	}
	key, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	entry, _ := potentialEffectsCatalogs.LoadOrStore(filepath.Clean(key), &potentialEffectsCatalog{})
	c := entry.(*potentialEffectsCatalog)
	c.once.Do(func() { c.catalog, c.err = LoadPotentialCatalog(key) })
	return c.catalog, c.err
}

// ResolvePotentialEffects reads only current physical, noncash, revealed worn
// sources. Expired/hidden/bag/cash equipment contributes nothing and does not
// require a mounted catalog. The original option bit patterns stay untouched.
func ResolvePotentialEffects(root string, c domain.Character, now time.Time) (PotentialEffects, error) {
	out := PotentialEffects{}
	var catalog *PotentialCatalog
	seenSlots := map[int]bool{}
	for _, e := range c.Equipment {
		if e.Slot >= 0 || e.Slot <= -100 || e.CashID != 0 || e.ExpiresAt > 0 && e.ExpiresAt <= now.UnixMilli() ||
			e.Potential.RawState&0x10 == 0 {
			continue
		}
		if seenSlots[e.Slot] {
			return PotentialEffects{}, fmt.Errorf("duplicate potential worn slot %d", e.Slot)
		}
		seenSlots[e.Slot] = true
		def, err := ItemDefinition(root, e.ItemID)
		if err != nil {
			return PotentialEffects{}, err
		}
		if def.Cash {
			continue
		}
		if e.Potential.RawState&15 < 1 || e.Potential.RawState&15 > 4 {
			return PotentialEffects{}, fmt.Errorf("unsupported revealed potential state 0x%02x", e.Potential.RawState)
		}
		if catalog == nil {
			catalog, err = cachedPotentialEffectsCatalog(root)
			if err != nil {
				return PotentialEffects{}, err
			}
			out.SourceSHA256 = catalog.sha256
			out.Values = make(map[string]int)
		}
		target := PotentialTarget{RequiredLevel: def.ReqLevel, ItemFlags: uint16(e.Flags)}
		if e.Flags < 0 || e.Flags > 65535 {
			return PotentialEffects{}, fmt.Errorf("invalid general equipment flags")
		}
		for i := 0; i < def.FixedPotentialOptionCount; i++ {
			target.OptionLevelOverrides[i] = def.FixedPotentialOptions[i].Level
		}
		for i, word := range e.Potential.Options {
			if word == 0 {
				continue
			}
			band, err := PotentialStatBand(target, i)
			if err != nil {
				return PotentialEffects{}, err
			}
			option, ok := catalog.options[int(word)]
			if !ok {
				return PotentialEffects{}, fmt.Errorf("unknown revealed potential option %d", word)
			}
			attributes, ok := option.Levels[band]
			if !ok {
				return PotentialEffects{}, fmt.Errorf("potential option %d missing band %d", word, band)
			}
			line := PotentialEffectLine{EquipmentID: e.ID, Slot: e.Slot, Line: i + 1, OptionID: int(word), Band: band, Attributes: clonePotentialLevel(attributes)}
			nativeContact, nativeContactKnown := nativeContactDamagePotential(line)
			if nativeContactKnown {
				out.NativeContactDamage = append(out.NativeContactDamage, nativeContact)
			}
			attackProc, attackProcKnown := attackProcPotential(line)
			if attackProcKnown {
				out.AttackProcs = append(out.AttackProcs, attackProc)
			}
			pending := PendingPotentialEffect{EquipmentID: e.ID, Line: i + 1, OptionID: int(word)}
			for key, value := range attributes.Values {
				support, ok := potentialEffectSupport[key]
				if !ok {
					return PotentialEffects{}, fmt.Errorf("unknown potential attribute %s", key)
				}
				// Only exact, consumed line shapes are recognized.
				// Global prop/ignoreDAM/attackType coverage stays pending for other shapes;
				// aggregate Values must never drive a second server roll.
				if support == PotentialPending && value != 0 && !nativeContactKnown && !attackProcKnown {
					pending.Attributes = append(pending.Attributes, key)
				}
				next := int64(out.Values[key]) + int64(value)
				if next < -1<<31 || next > 1<<31-1 {
					return PotentialEffects{}, fmt.Errorf("potential aggregate overflow %s", key)
				}
				out.Values[key] = int(next)
			}
			for key, value := range attributes.Text {
				if potentialEffectSupport[key] != PotentialPending || key != "face" {
					return PotentialEffects{}, fmt.Errorf("unknown potential text attribute %s", key)
				}
				if value != "" {
					pending.Attributes = append(pending.Attributes, key)
				}
			}
			if len(pending.Attributes) > 0 {
				sort.Strings(pending.Attributes)
				out.Pending = append(out.Pending, pending)
			}
			if percent := attributes.Values["incDAMr"]; percent != 0 {
				out.BossDamagePercents = append(out.BossDamagePercents, percent)
				if attributes.Values["boss"] <= 0 {
					out.DamagePercents = append(out.DamagePercents, percent)
				}
			}
			out.Lines = append(out.Lines, line)
		}
	}
	return out, nil
}
