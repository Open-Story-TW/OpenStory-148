// Package settings holds the operator-editable server settings. A setting
// takes its saved value from ops_server_settings; without a saved value it
// keeps the value its environment variable gives it, so an installation that
// never opens the web admin behaves as before.
package settings

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type Kind string

const (
	Bool Kind = "bool"
	Int  Kind = "int"
	Text Kind = "text"
	// Fees is a fee table in the text form domain.ParseFeeTiers reads; Max is
	// the number of tiers it may have.
	Fees Kind = "fees"
)

// Def describes one setting. A Live setting takes effect in running services
// within a few seconds; the others change packet layouts and are read once
// when a service starts. Client marks the settings the launcher follows.
type Def struct {
	Key     string `json:"key"`
	Group   string `json:"group"`
	Label   string `json:"label"`
	Help    string `json:"help"`
	Kind    Kind   `json:"kind"`
	Default string `json:"default"`
	Env     string `json:"env"`
	Min     int64  `json:"min,omitempty"`
	Max     int64  `json:"max,omitempty"`
	Live    bool   `json:"live"`
	Client  bool   `json:"client"`
}

const (
	groupProtocol = "protocol"
	groupGameplay = "gameplay"
	groupTrade    = "trade"
	groupClient   = "client"
	groupCombat   = "combat"
	groupRates    = "rates"
	groupDebug    = "debug"
	groupLogin    = "login"
)

var defs = []Def{
	{Key: "int_level", Group: groupProtocol, Label: "等級突破 255", Help: "關閉時等級上限回到原版 200。", Kind: Bool, Default: "1", Env: "MAPLEGO_INT_LEVEL", Client: true},
	{Key: "max_level", Group: groupProtocol, Label: "等級上限", Help: "等級突破開啟時的最高等級。", Kind: Int, Default: "999", Env: "MAPLEGO_MAX_LEVEL", Min: 256, Max: 2147483647, Client: true},
	{Key: "long_stats", Group: groupProtocol, Label: "能力值突破 32767", Help: "力量、敏捷、智力、幸運與 AP 改用 4 位元組。", Kind: Bool, Default: "1", Env: "MAPLEGO_LONG_STATS", Client: true},
	{Key: "long_vitals", Group: groupProtocol, Label: "HP/MP 突破 99,999", Help: "最大 HP 與 MP 上限從 99,999 提高到 9,999,999。", Kind: Bool, Default: "1", Env: "MAPLEGO_LONG_VITALS", Client: true},
	{Key: "long_experience", Group: groupProtocol, Label: "64 位元經驗值", Help: "經驗值可超過 21 億。", Kind: Bool, Default: "1", Env: "MAPLEGO_LONG_EXPERIENCE", Client: true},
	{Key: "long_money", Group: groupProtocol, Label: "楓幣與點數突破 21 億", Help: "楓幣、倉庫楓幣、Gash 與楓點的餘額可超過 21 億；單筆金額仍在 21 億內。", Kind: Bool, Default: "1", Env: "MAPLEGO_LONG_MONEY", Client: true},
	{Key: "long_damage", Group: groupProtocol, Label: "傷害突破 21 億", Help: "單行傷害可超過 32 位元上限。", Kind: Bool, Default: "1", Env: "MAPLEGO_LONG_DAMAGE", Client: true},
	{Key: "large_guild", Group: groupProtocol, Label: "公會人數突破 255", Help: "公會人數改用 2 位元組，公會人數上限可以超過 200。關閉時上限回到原版 200。", Kind: Bool, Default: "1", Env: "MAPLEGO_LARGE_GUILD", Client: true},
	{Key: "large_buddy", Group: groupProtocol, Label: "好友人數突破 255", Help: "好友人數改用 2 位元組，好友人數上限可以超過 255。關閉時上限回到 255。", Kind: Bool, Default: "1", Env: "MAPLEGO_LARGE_BUDDY", Client: true},
	{Key: "long_damage_cap", Group: groupProtocol, Label: "單行傷害上限", Help: "傷害突破開啟時的每行上限。", Kind: Int, Default: strconv.FormatInt(protocol.LongDamageMaxCap, 10), Env: "MAPLEGO_LONG_DAMAGE_CAP", Min: 1, Max: protocol.LongDamageMaxCap, Client: true},

	{Key: "exp_rate", Group: groupRates, Label: "經驗倍率", Help: "打怪經驗值的倍數。", Kind: Int, Default: "1", Env: "MAPLEGO_EXP_RATE", Min: 1, Max: domain.MaxRate, Live: true},
	{Key: "meso_rate", Group: groupRates, Label: "楓幣倍率", Help: "怪物掉落楓幣的倍數。", Kind: Int, Default: "1", Env: "MAPLEGO_MESO_RATE", Min: 1, Max: domain.MaxRate, Live: true},
	{Key: "drop_rate", Group: groupRates, Label: "掉寶倍率", Help: "怪物掉落道具機率的倍數。", Kind: Int, Default: "1", Env: "MAPLEGO_DROP_RATE", Min: 1, Max: domain.MaxRate, Live: true},

	{Key: "stack_buffs", Group: groupGameplay, Label: "Buff 疊加", Help: "不同技能與道具的攻擊、魔攻、命中、迴避、速度、跳躍可以疊加。", Kind: Bool, Default: "1", Env: "MAPLEGO_STACK_BUFFS", Live: true},
	{Key: "relog_buffs", Group: groupGameplay, Label: "重登保留 Buff", Help: "重新登入後保留剩餘時間內的 Buff 與召喚獸。", Kind: Bool, Default: "1", Env: "MAPLEGO_RELOG_BUFFS", Live: true},
	{Key: "guild_max_capacity", Group: groupGameplay, Label: "公會人數上限", Help: "每個公會最多幾人；GP 擴充可以加到這個數字，楓幣擴充最多到 100。調低時超過的公會不能再收新人，但不會踢人。", Kind: Int, Default: strconv.Itoa(domain.DefaultGuildCapacityLimit), Env: "MAPLEGO_GUILD_MAX_CAPACITY", Min: domain.GuildStartCapacity, Max: domain.MaxGuildCapacityLimit, Live: true},
	{Key: "guild_base_capacity", Group: groupGameplay, Label: "公會基本人數", Help: "每個公會至少可以有幾人，不用到公會本部擴充；原版是 10。設成和人數上限一樣時所有公會直接開滿。", Kind: Int, Default: strconv.Itoa(domain.GuildStartCapacity), Env: "MAPLEGO_GUILD_BASE_CAPACITY", Min: 1, Max: domain.MaxGuildCapacityLimit, Live: true},
	{Key: "buddy_capacity", Group: groupGameplay, Label: "好友人數上限", Help: "每個角色最多幾位好友；原版是 232。調低時已有的好友不會被刪，只是不能再加。", Kind: Int, Default: strconv.Itoa(domain.DefaultBuddyCapacityLimit), Env: "MAPLEGO_BUDDY_CAPACITY", Min: 1, Max: domain.MaxBuddyCapacityLimit, Live: true},
	{Key: "enable_potential", Group: groupGameplay, Label: "潛能功能", Help: "開放潛能卷軸與放大鏡。", Kind: Bool, Default: "1", Env: "MAPLEGO_ENABLE_POTENTIAL", Live: true},

	{Key: "merchant_fee_rates", Group: groupTrade, Label: "精靈商人手續費", Help: "賣家每筆售出依成交金額收的手續費。最多 6 級；沒有任何級距表示不收。", Kind: Fees, Default: domain.DefaultMerchantFees, Env: "MAPLEGO_MERCHANT_FEE_RATES", Max: domain.MaxFeeTiers, Live: true, Client: true},
	{Key: "trade_fee_rates", Group: groupTrade, Label: "交易手續費", Help: "玩家交易時，依收到的楓幣金額收的手續費。最多 6 級；沒有任何級距表示不收。", Kind: Fees, Default: domain.DefaultTradeFees, Env: "MAPLEGO_TRADE_FEE_RATES", Max: domain.MaxFeeTiers, Live: true, Client: true},
	{Key: "trade_cash_items", Group: groupTrade, Label: "玩家交易可放現金道具", Help: "開啟後現金道具可以放進玩家交易（寵物與商店許可證除外）。", Kind: Bool, Default: "0", Env: "MAPLEGO_TRADE_CASH_ITEMS", Live: true, Client: true},
	{Key: "merchant_cash_items", Group: groupTrade, Label: "精靈商人可賣現金道具", Help: "開啟後現金道具可以上架到精靈商人（寵物與商店許可證除外）。", Kind: Bool, Default: "0", Env: "MAPLEGO_MERCHANT_CASH_ITEMS", Live: true, Client: true},
	{Key: "parcel_delay_minutes", Group: groupTrade, Label: "宅配送達時間（分鐘）", Help: "一般宅配寄出後要多久才能收件；0 是立即送達，快速配送一律立即送達。宅配視窗上的「需要消耗幾小時」會跟著改。", Kind: Int, Default: "60", Env: "MAPLEGO_PARCEL_DELAY_MINUTES", Min: 0, Max: 10080, Live: true, Client: true},
	{Key: "merchant_price_units", Group: groupTrade, Label: "商店價格顯示單位", Help: "精靈商人內的價格加上兆、億、萬，例如 5兆1300億4200萬2123，避免看錯位數。", Kind: Bool, Default: "1", Env: "MAPLEGO_MERCHANT_PRICE_UNITS", Live: true, Client: true},

	{Key: "equip_tooltip", Group: groupClient, Label: "新版裝備說明", Help: "裝備說明改成分區排版：需求、職業、裝備數值、道具強化。", Kind: Bool, Default: "1", Env: "MAPLEGO_EQUIP_TOOLTIP", Live: true, Client: true},
	{Key: "equip_compare", Group: groupClient, Label: "裝備能力比較", Help: "道具欄的裝備說明顯示與身上同部位裝備的能力差距。", Kind: Bool, Default: "1", Env: "MAPLEGO_EQUIP_COMPARE", Live: true, Client: true},
	{Key: "chat_unlimited", Group: groupClient, Label: "解除洗頻限制", Help: "聊天不再有連續發言的冷卻（原本 2 秒內第 4 句會被擋），伺服器的聊天頻率限制也一併放行（一般、好友、隊伍、公會、聯盟聊天）。GM 禁言照常有效。", Kind: Bool, Default: "1", Env: "MAPLEGO_CHAT_UNLIMITED", Live: true, Client: true},
	{Key: "chat_repeat_unlimited", Group: groupClient, Label: "解除重複發言限制", Help: "同一句話可以連續發送超過三次（原本第 4 次會被擋）。", Kind: Bool, Default: "1", Env: "MAPLEGO_CHAT_REPEAT_UNLIMITED", Live: true, Client: true},
	{Key: "skill_any_order", Group: groupClient, Label: "解除技能加點順序限制", Help: "前一轉的技能沒點滿也能直接點二轉、三轉、四轉的技能，不再出現「一轉技能點數不足」。技能本身的前置技能、等級與上限照常有效。", Kind: Bool, Default: "1", Env: "MAPLEGO_SKILL_ANY_ORDER", Live: true, Client: true},

	{Key: "ranged_no_melee", Group: groupCombat, Label: "遠攻不揮空", Help: "弓、弩、拳套、火槍一律射出，怪物貼近時不再改成近身揮擊。", Kind: Bool, Default: "0", Env: "MAPLEGO_RANGED_NO_MELEE", Live: true, Client: true},
	{Key: "jump_attack_all_jobs", Group: groupCombat, Label: "全職業跳躍攻擊", Help: "所有職業都能在跳躍中使用攻擊技能，和盜賊、煉獄巫師一樣。", Kind: Bool, Default: "0", Env: "MAPLEGO_JUMP_ATTACK_ALL_JOBS", Live: true, Client: true},
	{Key: "air_cast", Group: groupCombat, Label: "空中施放", Help: "召喚、騎乘與變形（機甲、美洲豹）、限地面的攻擊與位移技能都能在空中使用。", Kind: Bool, Default: "0", Env: "MAPLEGO_AIR_CAST", Live: true, Client: true},
	{Key: "air_teleport", Group: groupCombat, Label: "法師空中瞬移", Help: "瞬間移動（法師、龍魔導士、煉獄巫師）可以在跳躍中使用。", Kind: Bool, Default: "0", Env: "MAPLEGO_AIR_TELEPORT", Live: true, Client: true},
	{Key: "morph_skills", Group: groupCombat, Label: "全職業解除變身前置", Help: "變身狀態下可以直接攻擊與使用技能，不必先解除變身。", Kind: Bool, Default: "0", Env: "MAPLEGO_MORPH_SKILLS", Live: true, Client: true},
	{Key: "no_hit_stun", Group: groupCombat, Label: "解除受擊無法操作", Help: "被怪物打到時不會被擊退、不會失去控制，效果同格擋（Power Stance）。", Kind: Bool, Default: "0", Env: "MAPLEGO_NO_HIT_STUN", Live: true, Client: true},

	{Key: "trace_characters", Group: groupDebug, Label: "封包追蹤角色", Help: "要記錄封包的角色 ID，以逗號分隔；角色重新進入頻道後生效。", Kind: Text, Default: "", Env: "MAPLEGO_TRACE_CHARACTERS", Live: true},
	{Key: "trace_full_packets", Group: groupDebug, Label: "記錄完整封包內容", Help: "被追蹤的角色記錄完整封包內容。", Kind: Bool, Default: "0", Env: "MAPLEGO_TRACE_FULL_PACKETS", Live: true},

	{Key: "character_slots", Group: groupLogin, Label: "每個世界的角色上限", Help: "每個帳號在一個世界最多能建立幾個角色，超過 15 需要用登入器。調低後已經建立的角色照樣能顯示、登入和遊玩，只是不能再建立新角色。", Kind: Int, Default: strconv.Itoa(domain.DefaultCharacterSlots), Env: "MAPLEGO_CHARACTER_SLOTS", Min: 1, Max: domain.MaxCharacterSlots, Live: true},
}

// combatSwitches are the client-side combat and movement limits the
// launcher lifts. The server has no check of its own for any of them:
// attacks are validated by position and range, never by foothold, morph or
// knockback state.
var combatSwitches = []struct {
	key string
	bit uint32
}{
	{"ranged_no_melee", protocol.ClientUIRangedNoMelee},
	{"jump_attack_all_jobs", protocol.ClientUIJumpAttackAllJobs},
	{"air_cast", protocol.ClientUIAirCast},
	{"air_teleport", protocol.ClientUIAirTeleport},
	{"morph_skills", protocol.ClientUIMorphSkills},
	{"no_hit_stun", protocol.ClientUINoHitStun},
}

// Defs lists every setting in display order.
func Defs() []Def { return append([]Def(nil), defs...) }

func find(key string) (Def, bool) {
	for _, d := range defs {
		if d.Key == key {
			return d, true
		}
	}
	return Def{}, false
}

// Normalize validates a value for a setting and returns its stored form.
func Normalize(key, value string) (string, error) {
	d, ok := find(key)
	if !ok {
		return "", fmt.Errorf("unknown setting %q", key)
	}
	value = strings.TrimSpace(value)
	switch d.Kind {
	case Bool:
		switch strings.ToLower(value) {
		case "1", "true", "on":
			return "1", nil
		case "0", "false", "off":
			return "0", nil
		}
		return "", fmt.Errorf("%s 必須是開或關", d.Label)
	case Int:
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < d.Min || n > d.Max {
			return "", fmt.Errorf("%s 必須是 %d 到 %d 的整數", d.Label, d.Min, d.Max)
		}
		return strconv.FormatInt(n, 10), nil
	case Fees:
		tiers, err := domain.ParseFeeTiers(value)
		if err != nil {
			return "", fmt.Errorf("%s 的級距金額要由小到大，最多 %d 級，百分比 0 到 100、最多三位小數", d.Label, d.Max)
		}
		return domain.FormatFeeTiers(tiers), nil
	default:
		if d.Key == "trace_characters" {
			var ids []string
			for _, part := range strings.Split(value, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				id, err := strconv.ParseInt(part, 10, 64)
				if err != nil || id < 1 {
					return "", fmt.Errorf("%s 必須是以逗號分隔的角色 ID", d.Label)
				}
				ids = append(ids, strconv.FormatInt(id, 10))
			}
			if len(ids) > 32 {
				return "", fmt.Errorf("%s 最多 32 個", d.Label)
			}
			return strings.Join(ids, ","), nil
		}
		if len(value) > 1024 {
			return "", fmt.Errorf("%s 太長", d.Label)
		}
		return value, nil
	}
}

// environment is the value a setting has without a saved row. The process
// environment is read once, before any live setting is written back to it.
var environment = func() map[string]string {
	out := make(map[string]string, len(defs))
	for _, d := range defs {
		raw := os.Getenv(d.Env)
		value := d.Default
		switch d.Kind {
		case Bool:
			// Default-on switches turn off only on "0"; default-off ones
			// turn on only on "1", as each did before settings were saved.
			if d.Default == "1" {
				value = "1"
				if raw == "0" {
					value = "0"
				}
			} else if raw == "1" {
				value = "1"
			}
		default:
			if raw != "" {
				if normal, err := Normalize(d.Key, raw); err == nil {
					value = normal
				}
			}
		}
		out[d.Key] = value
	}
	return out
}()

// Resolve merges saved rows over the environment defaults. Unknown keys and
// values that no longer validate are ignored.
func Resolve(saved map[string]string) map[string]string {
	out := make(map[string]string, len(defs))
	for _, d := range defs {
		out[d.Key] = environment[d.Key]
		if raw, ok := saved[d.Key]; ok {
			if normal, err := Normalize(d.Key, raw); err == nil {
				out[d.Key] = normal
			}
		}
	}
	return out
}

// Saved reads the saved rows.
func Saved(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT key,value FROM ops_server_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err = rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// Save validates and stores the given settings in one transaction.
func Save(ctx context.Context, db *sql.DB, actor string, values map[string]string) error {
	normal := make(map[string]string, len(values))
	for key, value := range values {
		v, err := Normalize(key, value)
		if err != nil {
			return err
		}
		normal[key] = v
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range normal {
		if _, err = tx.ExecContext(ctx, `INSERT INTO ops_server_settings(key,value,updated_by) VALUES($1,$2,$3) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_by=excluded.updated_by,updated_at=clock_timestamp()`, key, value, actor); err != nil {
			return err
		}
	}
	return tx.Commit()
}

var (
	mu     sync.Mutex
	active = map[string]string{}
)

// Active is the value each setting currently has in this process.
func Active() map[string]string {
	mu.Lock()
	defer mu.Unlock()
	out := make(map[string]string, len(active))
	for k, v := range active {
		out[k] = v
	}
	return out
}

func on(values map[string]string, key string) bool { return values[key] == "1" }
func number(values map[string]string, key string) int64 {
	n, _ := strconv.ParseInt(values[key], 10, 64)
	return n
}

// apply puts resolved values into effect. Only a service that has not yet
// accepted a connection may pass startup: the wire contracts cannot change
// under a connected client.
func apply(values map[string]string, startup bool) {
	mu.Lock()
	defer mu.Unlock()
	if startup {
		limit := 0
		if on(values, "int_level") {
			limit = int(number(values, "max_level"))
		}
		domain.SetIntLevelCap(limit)
		domain.SetLongStats(on(values, "long_stats"))
		domain.SetLongVitals(on(values, "long_vitals"))
		domain.SetLongExperience(on(values, "long_experience"))
		domain.SetLongMoney(on(values, "long_money"))
		domain.SetLargeGuild(on(values, "large_guild"))
		domain.SetLargeBuddy(on(values, "large_buddy"))
		var damageCap int64
		if on(values, "long_damage") {
			damageCap = number(values, "long_damage_cap")
		}
		protocol.SetLongDamageCap(damageCap)
	}
	domain.SetRates(int(number(values, "exp_rate")), int(number(values, "meso_rate")), int(number(values, "drop_rate")))
	domain.SetStackBuffs(on(values, "stack_buffs"))
	domain.SetBuddyCapacity(int(number(values, "buddy_capacity")))
	domain.SetGuildCapacity(int(number(values, "guild_max_capacity")), int(number(values, "guild_base_capacity")))
	domain.SetRelogBuffs(on(values, "relog_buffs"))
	var ui uint32
	if on(values, "equip_tooltip") {
		ui |= protocol.ClientUIEquipTooltip
	}
	if on(values, "equip_compare") {
		ui |= protocol.ClientUIEquipCompare
	}
	if on(values, "merchant_price_units") {
		ui |= protocol.ClientUIMerchantPriceUnits
	}
	if on(values, "merchant_cash_items") {
		ui |= protocol.ClientUIMerchantCashItems
	}
	if on(values, "trade_cash_items") {
		ui |= protocol.ClientUITradeCashItems
	}
	for _, sw := range combatSwitches {
		if on(values, sw.key) {
			ui |= sw.bit
		}
	}
	if on(values, "chat_unlimited") {
		ui |= protocol.ClientUIChatUnlimited
	}
	if on(values, "chat_repeat_unlimited") {
		ui |= protocol.ClientUIChatRepeat
	}
	if on(values, "skill_any_order") {
		ui |= protocol.ClientUISkillAnyOrder
	}
	domain.SetChatUnlimited(on(values, "chat_unlimited"))
	protocol.SetClientUI(ui)
	domain.SetParcelDelay(time.Duration(number(values, "parcel_delay_minutes")) * time.Minute)
	domain.SetMerchantCashItems(on(values, "merchant_cash_items"))
	domain.SetTradeCashItems(on(values, "trade_cash_items"))
	// Normalize already vetted these; a table that still fails keeps the old one.
	if tiers, err := domain.ParseFeeTiers(values["merchant_fee_rates"]); err == nil {
		domain.SetMerchantFees(tiers)
	}
	if tiers, err := domain.ParseFeeTiers(values["trade_fee_rates"]); err == nil {
		domain.SetTradeFees(tiers)
	}
	domain.SetCharacterSlots(int(number(values, "character_slots")))
	// These three are read from the environment where they are used.
	os.Setenv("MAPLEGO_ENABLE_POTENTIAL", values["enable_potential"])
	os.Setenv("MAPLEGO_TRACE_CHARACTERS", values["trace_characters"])
	os.Setenv("MAPLEGO_TRACE_FULL_PACKETS", values["trace_full_packets"])
	for _, d := range defs {
		if startup || d.Live {
			active[d.Key] = values[d.Key]
		}
	}
}

// Start applies the saved settings before the service accepts connections,
// then keeps the live ones current until ctx ends. A database that has no
// settings table yet leaves the environment defaults in force.
func Start(ctx, first context.Context, db *sql.DB) {
	saved, err := Saved(first, db)
	if err != nil {
		slog.Warn("settings_unavailable", "error", err)
	}
	apply(Resolve(saved), true)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				read, cancel := context.WithTimeout(ctx, 3*time.Second)
				saved, err := Saved(read, db)
				cancel()
				if err == nil {
					apply(Resolve(saved), false)
				}
			}
		}
	}()
}
