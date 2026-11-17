// Profession (生活技能) wire layouts for the v148 client, read from the
// client's own senders and handlers; the address beside each layout is the
// client function that builds or decodes it.
package protocol

import (
	"strconv"
	"strings"

	"maple-go-server/internal/domain"
)

const (
	RecvUseRecipe      uint16 = 0x83  // 0x9BBD45
	RecvProfessionInfo uint16 = 0xc7  // 0x9EBC14
	RecvCraftDone      uint16 = 0xff  // 0x7D3DDB
	RecvCraftEffect    uint16 = 0x100 // 0x905F35
	RecvCraftMake      uint16 = 0x101 // 0x8F21B5
	RecvReactorHit     uint16 = 0x17d // 0x68F640, 0x6902A5
	RecvReactorClick   uint16 = 0x17e // type 9: reactor object ID, 0x68FC65
	RecvStartHarvest   uint16 = 0x1a1 // 0x8F2124
	RecvStopHarvest    uint16 = 0x1a2 // 0x8F2124

	SendProfessionInfo uint16 = 0x8e  // 0x9EC156
	SendCraftStarted   uint16 = 0x113 // 0x8BD72C
	SendCraftComplete  uint16 = 0x114 // 0x8BE157
	SendHarvested      uint16 = 0x115 // 0x8BE2CE
	SendShowHarvesting uint16 = 0x14b // 0x9474F9
	SendOpenUI         uint16 = 0x162 // 0x8ECE01
	SendHarvestMessage uint16 = 0x178 // 0x8FF5FF
	SendReactorState   uint16 = 0x1ed // 0x68EB85
	SendReactorEnter   uint16 = 0x1ef // 0x691CBF
	SendReactorLeave   uint16 = 0x1f1 // 0x68EC48
)

// Stat-changed mask bits of GW_CharacterStat::DecodeChangeStat (0x4D1617).
// Fatigue is one byte; each trait is its total experience as an int, in the
// client's order: charisma, insight, will, craft, sense, charm.
const (
	statFatigue  uint64 = 0x80000
	statCharisma uint64 = 0x100000
	statInsight  uint64 = 0x200000
	statWill     uint64 = 0x400000
	statCraft    uint64 = 0x800000
	statSense    uint64 = 0x1000000
	statCharm    uint64 = 0x2000000
)

// uiMakingSkill is CUIMakingSkill, the 生活技能 window (CWvsContext::UI_Open
// case 42, 0x9BAA2C).
const uiMakingSkill = 42

// Harvest message codes (0x8FF5FF): Start begins the gathering animation, the
// others show the client's own refusal text.
const (
	HarvestNoTool     = 1
	HarvestLowLevel   = 2
	HarvestNoHerbs    = 3
	HarvestNoMining   = 4
	HarvestTired      = 5
	HarvestTooFar     = 6
	HarvestBusy       = 8
	HarvestNotYet     = 9
	HarvestUnknown    = 10
	HarvestStart      = 11
	CraftGood         = 21 // success; the client draws 20 as SO SO, 21 as GOOD and 22 as COOL
	CraftUnknownError = 23 // 因未知理由而失敗。
	CraftFailed       = 24 // 道具製作失敗。
)

func traitValues(t domain.Traits) [6]int {
	return [6]int{t.Charisma, t.Insight, t.Will, t.Craft, t.Sense, t.Charm}
}

// addTraitStats writes the fatigue byte, its date stamp and the six trait
// totals of GW_CharacterStat::Decode (0x4D122D).
func addTraitStats(w *Writer, c domain.Character, date int32) {
	w.U8(byte(max(0, min(c.Fatigue, 255))))
	w.I32(date)
	for _, v := range traitValues(c.Traits) {
		w.I32(int32(v))
	}
}

// professionSkillValue is how the client keeps a profession in its skill
// records: level in the top byte, proficiency in the low 24 bits (0x6D447A
// reads the level, 0x6D735A the proficiency).
func professionSkillValue(p domain.ProfessionState) int32 {
	return int32(uint32(p.Level)<<24 | uint32(p.EXP)&0xffffff)
}

// wireSkills is the skill record list the client sees: the job skills, each
// profession, and every learned recipe as a level-1 record of the recipe ID.
func wireSkills(c domain.Character) []domain.SkillState {
	if len(c.Professions) == 0 && len(c.Recipes) == 0 {
		return c.Skills
	}
	out := append([]domain.SkillState(nil), c.Skills...)
	for _, p := range c.Professions {
		out = append(out, domain.SkillState{ID: p.ID, Level: int(professionSkillValue(p))})
	}
	for _, r := range c.Recipes {
		out = append(out, domain.SkillState{ID: r, Level: 1})
	}
	return out
}

type skillRecord struct {
	id    int
	level int32
}

// skillRecords is the change-skill-record packet (0x9D835D) for several
// records at once; a level of -1 makes the client forget a recipe.
func skillRecords(records []skillRecord) []byte {
	w := NewWriter(0x31)
	w.U8(0)
	w.U8(0)
	w.U16(uint16(len(records)))
	for _, r := range records {
		w.I32(int32(r.id))
		w.I32(r.level)
		w.I32(0)
		w.I64(maxTime)
	}
	w.U8(4)
	return w.Packet()
}

// ProfessionChanges lists the packets that bring the client from before to
// after: profession and recipe skill records, then fatigue and traits, then
// the "trait gained" chat line.
func ProfessionChanges(before, after domain.Character) [][]byte {
	var records []skillRecord
	for _, p := range after.Professions {
		if before.Profession(p.ID) != p {
			records = append(records, skillRecord{p.ID, professionSkillValue(p)})
		}
	}
	for _, p := range before.Professions {
		if after.Profession(p.ID).Level == 0 {
			records = append(records, skillRecord{p.ID, 0})
		}
	}
	for _, r := range after.Recipes {
		if !before.KnowsRecipe(r) {
			records = append(records, skillRecord{r, 1})
		}
	}
	for _, r := range before.Recipes {
		if !after.KnowsRecipe(r) {
			records = append(records, skillRecord{r, -1})
		}
	}
	var out [][]byte
	if len(records) > 0 {
		out = append(out, skillRecords(records))
	}
	if p := TraitStats(before, after); p != nil {
		out = append(out, p)
	}
	if p := TraitGain(before.Traits, after.Traits); p != nil {
		out = append(out, p)
	}
	return out
}

// TraitStats is the stat-changed packet for fatigue and the traits that
// differ between before and after, or nil when none does.
func TraitStats(before, after domain.Character) []byte {
	var mask uint64
	if before.Fatigue != after.Fatigue {
		mask |= statFatigue
	}
	old, now := traitValues(before.Traits), traitValues(after.Traits)
	for i := range now {
		if old[i] != now[i] {
			mask |= statCharisma << i
		}
	}
	if mask == 0 {
		return nil
	}
	w := NewWriter(SendUpdateStats)
	w.U8(0)
	w.I64(int64(mask))
	if mask&statFatigue != 0 {
		w.U8(byte(max(0, min(after.Fatigue, 255))))
	}
	for i, v := range now {
		if mask&(statCharisma<<i) != 0 {
			w.I32(int32(v))
		}
	}
	w.U8(0)
	w.U8(0)
	return w.Packet()
}

// TraitGain is message type 16 (0x9D457F): one "trait (+n)" line per trait
// that grew. It returns nil when none did.
func TraitGain(before, after domain.Traits) []byte {
	var mask uint64
	old, now := traitValues(before), traitValues(after)
	for i := range now {
		if now[i] > old[i] {
			mask |= statCharisma << i
		}
	}
	if mask == 0 {
		return nil
	}
	w := NewWriter(0x36)
	w.U8(16)
	w.I64(int64(mask))
	for i := range now {
		if now[i] > old[i] {
			w.I32(int32(now[i] - old[i]))
		}
	}
	return w.Packet()
}

// OpenCraftingWindow opens the 生活技能 window.
func OpenCraftingWindow() []byte {
	w := NewWriter(SendOpenUI)
	w.I32(uiMakingSkill)
	return w.Packet()
}

// ProfessionInfoRequest asks for the success rate of a recipe: the profession
// skill ID as text (the client may append "_premium"), the recipe's required
// level minus one and the character's level minus one.
type ProfessionInfoRequest struct {
	Key        string
	Profession int
	Required   int32
	Current    int32
}

func ReadProfessionInfo(r *Reader) (ProfessionInfoRequest, error) {
	v := ProfessionInfoRequest{Key: r.String(), Required: r.I32(), Current: r.I32()}
	if r.Err() != nil || r.Remaining() != 0 || len(v.Key) < 8 || len(v.Key) > 32 {
		return v, ErrMalformed
	}
	id, err := strconv.Atoi(strings.TrimSuffix(v.Key, "_premium"))
	if err != nil {
		return v, ErrMalformed
	}
	v.Profession = id
	return v, nil
}

// ProfessionInfo answers a ProfessionInfoRequest. The first three fields echo
// the request: the client rebuilds its cache key from them.
func ProfessionInfo(q ProfessionInfoRequest, rate int) []byte {
	w := NewWriter(SendProfessionInfo)
	w.String(q.Key)
	w.I32(q.Required)
	w.I32(q.Current)
	w.U8(1)
	w.I32(int32(rate))
	return w.Packet()
}

// CraftStarted shows a character working at a craft for duration ms. action
// is the character action the client sent with its craft-make request.
func CraftStarted(characterID int64, action, duration int32) []byte {
	w := NewWriter(SendCraftStarted)
	w.I32(int32(characterID))
	w.I32(action)
	w.I32(duration)
	return w.Packet()
}

// CraftComplete ends a craft. Only a success (20-22) names the product;
// exp is the proficiency gained, drawn as a number over the character.
func CraftComplete(characterID int64, recipe, result, item, quantity, exp int) []byte {
	w := NewWriter(SendCraftComplete)
	w.I32(int32(characterID))
	w.I32(int32(recipe))
	w.I32(int32(result))
	if result >= 20 && result <= 22 {
		w.I32(int32(item))
		w.I32(int32(quantity))
	}
	w.I32(int32(exp))
	return w.Packet()
}

// Harvested ends a character's gathering animation.
func Harvested(characterID int64, success bool) []byte {
	w := NewWriter(SendHarvested)
	w.I32(int32(characterID))
	w.Bool(success)
	return w.Packet()
}

// ShowHarvesting shows another character gathering with a tool; tool 0 stops it.
func ShowHarvesting(characterID int64, tool int) []byte {
	w := NewWriter(SendShowHarvesting)
	w.I32(int32(characterID))
	w.Bool(tool != 0)
	if tool != 0 {
		w.I32(int32(tool))
		w.I32(0) // no vehicle
	}
	return w.Packet()
}

// HarvestMessage answers a start-harvest request with one of the Harvest codes.
func HarvestMessage(objectID int32, code int) []byte {
	w := NewWriter(SendHarvestMessage)
	w.I32(objectID)
	w.I32(int32(code))
	return w.Packet()
}

func ReactorEnter(objectID int32, templateID, state, x, y, facing int) []byte {
	w := NewWriter(SendReactorEnter)
	w.I32(objectID)
	w.I32(int32(templateID))
	w.U8(byte(state))
	w.I16(int16(x))
	w.I16(int16(y))
	w.U8(byte(facing))
	w.String("")
	return w.Packet()
}

// ReactorState moves a reactor to a state: hit delay 0, event 0, and the
// state's animation given four tenths of a second.
func ReactorState(objectID int32, state, x, y int) []byte {
	w := NewWriter(SendReactorState)
	w.I32(objectID)
	w.U8(byte(state))
	w.I16(int16(x))
	w.I16(int16(y))
	w.U16(0)
	w.U8(0)
	w.U8(4)
	return w.Packet()
}

func ReactorLeave(objectID int32, state, x, y int) []byte {
	w := NewWriter(SendReactorLeave)
	w.I32(objectID)
	w.U8(byte(state))
	w.I16(int16(x))
	w.I16(int16(y))
	return w.Packet()
}

// ReadReactorHit reads a reactor hit: object, option bits, delay and skill.
// Only the object matters to gathering.
func ReadReactorHit(r *Reader) (int32, error) {
	id := r.I32()
	r.I32()
	r.U16()
	r.I32()
	if r.Err() != nil || r.Remaining() > 16 {
		return 0, ErrMalformed
	}
	return id, nil
}

// CraftMake is the start of a craft: the character action and its duration.
func ReadCraftMake(r *Reader) (action, duration int32, err error) {
	action, duration = r.I32(), r.I32()
	if r.Err() != nil || r.Remaining() != 0 {
		return 0, 0, ErrMalformed
	}
	return action, duration, nil
}

// ReadCraftDone reads the recipe of a finished craft. Disassembly and fusion
// (recipe IDs ending 9000 and 9001) carry item fields after it.
func ReadCraftDone(r *Reader) (int, error) {
	id := int(r.I32())
	if r.Err() != nil || r.Remaining() > 24 {
		return 0, ErrMalformed
	}
	return id, nil
}

// ReadUseRecipe reads the use of a recipe item (251xxxx): tick, slot, item.
func ReadUseRecipe(r *Reader) (slot, item int, err error) {
	r.U32()
	slot, item = int(r.I16()), int(r.I32())
	if r.Err() != nil || r.Remaining() != 0 || slot < 1 || slot > 128 || item/10000 != 251 {
		return 0, 0, ErrMalformed
	}
	return slot, item, nil
}

// ReadHarvest reads the reactor of a start- or stop-harvest request.
func ReadHarvest(r *Reader) (int32, error) {
	id := r.I32()
	if r.Err() != nil || r.Remaining() != 0 {
		return 0, ErrMalformed
	}
	return id, nil
}
