package admin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/wz"
)

// One character's bags and balances, and the operator's edits to them. An
// online character is read from and changed through the world that holds it,
// so its client sees the change at once; an offline one is changed in the
// database. Either way the store decides which of the two a character is: it
// refuses an offline edit while any session owns the character.

// worldEdit outlasts the world's own deadline for one durable command.
var worldEdit = &http.Client{Timeout: 12 * time.Second}

// worldCall is world() for the character endpoints, which answer a refusal
// with a JSON error code the admin has to tell apart.
func (s *Server) worldCall(ctx context.Context, client *http.Client, worldID int, method, path string, body, out any) (code string, err error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://127.0.0.1:%d%s", 19081+worldID*2, path), reader)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.RPCToken)
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	limited := io.LimitReader(res.Body, 8<<20)
	if res.StatusCode != http.StatusOK {
		var refusal struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(limited).Decode(&refusal)
		if refusal.Error == "" {
			return "", fmt.Errorf("world %d %s: status %d", worldID, path, res.StatusCode)
		}
		return refusal.Error, nil
	}
	return "", json.NewDecoder(limited).Decode(out)
}

// Amounts and asset IDs travel as strings: mesos and both wallets are 64-bit,
// which a browser's numbers cannot hold exactly.
type equipmentView struct {
	ID           string   `json:"id"`
	ItemID       int      `json:"item_id"`
	Slot         int      `json:"slot"`
	STR          int      `json:"str"`
	DEX          int      `json:"dex"`
	INT          int      `json:"int"`
	LUK          int      `json:"luk"`
	HP           int      `json:"hp"`
	MP           int      `json:"mp"`
	WATK         int      `json:"watk"`
	MATK         int      `json:"matk"`
	WDEF         int      `json:"wdef"`
	MDEF         int      `json:"mdef"`
	ACC          int      `json:"acc"`
	AVOID        int      `json:"avoid"`
	Speed        int      `json:"speed"`
	Jump         int      `json:"jump"`
	UpgradeSlots int      `json:"upgrade_slots"`
	ScrollLevel  int      `json:"scroll_level"`
	Enhance      int      `json:"enhance"`
	Cash         bool     `json:"cash"`
	ExpiresAt    int64    `json:"expires_at,omitempty"`
	Tier         int      `json:"potential_tier"` // 0 none, 1..4 revealed rank, -1 not yet revealed
	Potential    []string `json:"potential,omitempty"`
}

type stackView struct {
	ID        string `json:"id"`
	ItemID    int    `json:"item_id"`
	InvType   int    `json:"inv_type"`
	Slot      int    `json:"slot"`
	Quantity  int    `json:"quantity"`
	Cash      bool   `json:"cash"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	// Pet marks a pet: shown in the cash bag, but not editable here.
	Pet     bool   `json:"pet,omitempty"`
	PetName string `json:"pet_name,omitempty"`
	PetLv   int    `json:"pet_level,omitempty"`
}

type itemMeta struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Desc        string `json:"desc,omitempty"`
	InvType     int    `json:"inv_type"`
	SlotMax     int    `json:"slot_max"`
	Cash        bool   `json:"cash,omitempty"`
	Pet         bool   `json:"pet,omitempty"`
	Known       bool   `json:"known"`
	ReqLevel    int    `json:"req_level,omitempty"`
	ReqJob      int    `json:"req_job,omitempty"`
	ReqSTR      int    `json:"req_str,omitempty"`
	ReqDEX      int    `json:"req_dex,omitempty"`
	ReqINT      int    `json:"req_int,omitempty"`
	ReqLUK      int    `json:"req_luk,omitempty"`
	ReqFame     int    `json:"req_fame,omitempty"`
	Only        bool   `json:"only,omitempty"`
	TradeBlock  bool   `json:"trade_block,omitempty"`
	Quest       bool   `json:"quest,omitempty"`
	AttackSpeed int    `json:"attack_speed,omitempty"`
	// Base is the equipment as the server would create it, for the picker.
	Base *equipmentView `json:"base,omitempty"`
}

type characterView struct {
	ID        int64            `json:"id"`
	Name      string           `json:"name"`
	Account   string           `json:"account"`
	AccountID int64            `json:"account_id"`
	WorldID   int              `json:"world_id"`
	Level     int              `json:"level"`
	Job       int              `json:"job"`
	MapID     int              `json:"map_id"`
	Online    bool             `json:"online"`
	Channel   int              `json:"channel"`
	Notice    string           `json:"notice,omitempty"`
	Mesos     string           `json:"mesos"`
	Gash      string           `json:"gash"`
	Points    string           `json:"points"`
	MesosMax  string           `json:"mesos_max"`
	Slots     [5]int           `json:"slots"`
	Equipment []equipmentView  `json:"equipment"`
	Items     []stackView      `json:"items"`
	Meta      map[int]itemMeta `json:"meta"`
	Icons     bool             `json:"icons"`
}

func wzRoot() string { return os.Getenv("MAPLEGO_WZ_ROOT") }

func metaOf(id int) itemMeta {
	root := wzRoot()
	m := itemMeta{ID: id, InvType: id / 1000000, SlotMax: 1, Pet: id/10000 == 500}
	if text, ok := content.ItemName(root, id); ok {
		m.Name, m.Desc = text.Name, text.Desc
	}
	if m.Name == "" {
		m.Name = "道具 " + strconv.Itoa(id)
	}
	if item, err := content.ItemDefinition(root, id); err == nil {
		m.Known = true
		m.InvType, m.SlotMax, m.Cash = item.InvType, item.SlotMax, item.Cash
		m.ReqLevel, m.ReqJob, m.ReqSTR, m.ReqDEX, m.ReqINT, m.ReqLUK, m.ReqFame = item.ReqLevel, item.ReqJob, item.ReqSTR, item.ReqDEX, item.ReqINT, item.ReqLUK, item.ReqFame
		m.Only, m.TradeBlock, m.Quest, m.AttackSpeed = item.Only, item.TradeBlock, item.Quest, item.AttackSpeed
		if item.InvType == 1 {
			base := equipmentOf(item.Equipment, nil)
			base.ItemID, base.Cash = id, item.Cash
			m.Base = &base
		}
	}
	return m
}

var potentialCatalog = sync.OnceValue(func() *content.PotentialCatalog {
	catalog, err := content.LoadPotentialCatalog(wzRoot())
	if err != nil {
		slog.Warn("admin_potential_catalog", "error", err)
		return nil
	}
	return catalog
})

// potentialText renders the lines the game shows under an equipment's
// potential: each option's WZ sentence with the numbers of the equipment's
// level band filled in.
func potentialText(e domain.Equipment) (tier int, lines []string) {
	state := int(e.Potential.RawState)
	if state == 1 {
		return -1, nil
	}
	if state < 17 || state > 20 {
		return 0, nil
	}
	tier = state - 16
	catalog := potentialCatalog()
	if catalog == nil {
		return tier, nil
	}
	target := content.PotentialTarget{ItemID: e.ItemID}
	if item, err := content.ItemDefinition(wzRoot(), e.ItemID); err == nil {
		target.RequiredLevel = item.ReqLevel
		if view, err := content.PotentialOperationTarget(e, item); err == nil {
			target = view.PolicyTarget
		}
	}
	for line, word := range e.Potential.Options {
		if word == 0 || word >= 60000 {
			continue
		}
		option, ok := catalog.Option(int(word))
		if !ok {
			lines = append(lines, "潛能 "+strconv.Itoa(int(word)))
			continue
		}
		text := option.Description
		if band, err := content.PotentialStatBand(target, line); err == nil {
			if level, ok := catalog.Level(int(word), band); ok {
				keys := make([]string, 0, len(level.Values))
				for key := range level.Values {
					keys = append(keys, key)
				}
				// Longest first: #incSTRr must not be read as #incSTR.
				sort.Slice(keys, func(i, j int) bool {
					return len(keys[i]) > len(keys[j]) || len(keys[i]) == len(keys[j]) && keys[i] < keys[j]
				})
				for _, key := range keys {
					text = strings.ReplaceAll(text, "#"+key, strconv.Itoa(level.Values[key]))
				}
			}
		}
		lines = append(lines, text)
	}
	return tier, lines
}

func equipmentOf(e domain.Equipment, lines []string) equipmentView {
	return equipmentView{ID: strconv.FormatInt(e.ID, 10), ItemID: e.ItemID, Slot: e.Slot,
		STR: e.STR, DEX: e.DEX, INT: e.INT, LUK: e.LUK, HP: e.HP, MP: e.MP, WATK: e.WATK, MATK: e.MATK, WDEF: e.WDEF, MDEF: e.MDEF,
		ACC: e.ACC, AVOID: e.AVOID, Speed: e.Speed, Jump: e.Jump, UpgradeSlots: e.UpgradeSlots, ScrollLevel: e.ScrollLevel,
		Enhance: int(e.Enhance), ExpiresAt: e.ExpiresAt, Potential: lines}
}

func viewOf(c domain.Character) characterView {
	v := characterView{ID: c.ID, Name: c.Name, AccountID: c.AccountID, WorldID: c.WorldID, Level: c.Level, Job: c.Job, MapID: c.MapID,
		Mesos: strconv.FormatInt(c.Mesos, 10), MesosMax: strconv.FormatInt(domain.MaxMoney(), 10), Slots: [5]int{c.Capacity(1), c.Capacity(2), c.Capacity(3), c.Capacity(4), c.Capacity(5)},
		Equipment: []equipmentView{}, Items: []stackView{}, Meta: map[int]itemMeta{}}
	meta := func(id int) itemMeta {
		m, ok := v.Meta[id]
		if !ok {
			m = metaOf(id)
			v.Meta[id] = m
		}
		return m
	}
	for _, e := range c.Equipment {
		tier, lines := potentialText(e)
		view := equipmentOf(e, lines)
		view.Tier, view.Cash = tier, e.CashID != 0 || meta(e.ItemID).Cash
		v.Equipment = append(v.Equipment, view)
	}
	for _, item := range c.Items {
		v.Items = append(v.Items, stackView{ID: strconv.FormatInt(item.ID, 10), ItemID: item.TemplateID, InvType: item.InvType, Slot: item.Slot,
			Quantity: item.Quantity, Cash: item.CashID != 0 || meta(item.TemplateID).Cash, ExpiresAt: item.ExpiresAt})
	}
	for _, pet := range c.Pets {
		meta(pet.TemplateID)
		v.Items = append(v.Items, stackView{ID: "pet-" + strconv.FormatInt(pet.CashID, 10), ItemID: pet.TemplateID, InvType: 5, Slot: pet.Slot,
			Quantity: 1, Cash: true, ExpiresAt: pet.ExpiresAt, Pet: true, PetName: pet.Name, PetLv: pet.Level})
	}
	return v
}

var errNoCharacter = errors.New("no such character")

// loadCharacter reads one character: the live copy from its world while it is
// online, the saved one otherwise.
func (s *Server) loadCharacter(ctx context.Context, id int64) (characterView, error) {
	var account string
	var accountID int64
	var worldID int
	err := s.db.QueryRowContext(ctx, `SELECT acc.name,c.account_id,c.world_id FROM game_characters c JOIN auth_accounts acc ON acc.id=c.account_id WHERE c.id=$1`, id).Scan(&account, &accountID, &worldID)
	if errors.Is(err, sql.ErrNoRows) {
		return characterView{}, errNoCharacter
	} else if err != nil {
		return characterView{}, err
	}
	var live struct {
		Channel   int              `json:"channel"`
		Character domain.Character `json:"character"`
	}
	code, err := s.worldCall(ctx, s.client, worldID, http.MethodGet, "/admin/characters?id="+strconv.FormatInt(id, 10), nil, &live)
	var v characterView
	if err == nil && code == "" {
		v = viewOf(live.Character)
		v.Online, v.Channel = true, live.Channel
	} else {
		c, e := s.repo.GetCharacter(ctx, id)
		if errors.Is(e, domain.ErrNotFound) {
			return characterView{}, errNoCharacter
		} else if e != nil {
			return characterView{}, e
		}
		v = viewOf(c)
		if err != nil || code != "not_online" {
			var online bool
			_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM (`+onlineSessions+`) online WHERE online.character_id=$1)`, id).Scan(&online)
			if online {
				v.Notice = "這個角色在線上，但世界服務沒有回應：下面是上一次存檔的內容，現在無法修改。"
			}
		}
	}
	v.Account, v.AccountID, v.WorldID = account, accountID, worldID
	if repo, ok := s.repo.(domain.AdminInventoryRepository); ok {
		wallet, err := repo.ReadAdminWallet(ctx, accountID)
		if err != nil {
			return characterView{}, err
		}
		v.Gash, v.Points = strconv.FormatInt(wallet.Credit, 10), strconv.FormatInt(wallet.Points, 10)
	}
	v.Icons = s.icons().available()
	return v, nil
}

func (s *Server) writeCharacter(w http.ResponseWriter, r *http.Request, id int64) {
	v, err := s.loadCharacter(r.Context(), id)
	if errors.Is(err, errNoCharacter) {
		writeError(w, http.StatusNotFound, "找不到這個角色")
		return
	} else if err != nil {
		slog.Error("admin_character_read", "character", id, "error", err)
		writeError(w, http.StatusInternalServerError, "讀取角色失敗")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) character(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "角色編號不正確")
		return
	}
	s.writeCharacter(w, r, id)
}

type editStats struct {
	STR, DEX, INT, LUK, HP, MP, WATK, MATK, WDEF, MDEF, ACC, AVOID, Speed, Jump int
	UpgradeSlots                                                                int `json:"upgrade_slots"`
	ScrollLevel                                                                 int `json:"scroll_level"`
}

func (st *editStats) equipment() *domain.Equipment {
	return &domain.Equipment{STR: st.STR, DEX: st.DEX, INT: st.INT, LUK: st.LUK, HP: st.HP, MP: st.MP, WATK: st.WATK, MATK: st.MATK,
		WDEF: st.WDEF, MDEF: st.MDEF, ACC: st.ACC, AVOID: st.AVOID, Speed: st.Speed, Jump: st.Jump, UpgradeSlots: st.UpgradeSlots, ScrollLevel: st.ScrollLevel}
}

type inventoryEdit struct {
	CharacterID int64      `json:"character_id"`
	Kind        string     `json:"kind"`
	InvType     int        `json:"inv_type"`
	AssetID     string     `json:"asset_id"`
	Stats       *editStats `json:"stats"`
	ItemID      int        `json:"item_id"`
	Quantity    int        `json:"quantity"`
	Amount      string     `json:"amount"`
	// Seen is the item as the page showed it when the operator made the edit.
	Seen *struct {
		ItemID   int        `json:"item_id"`
		Quantity int        `json:"quantity"`
		Stats    *editStats `json:"stats"`
	} `json:"seen"`
}

func editError(err error, code string) (int, string) {
	switch {
	case code == "changed", errors.Is(err, domain.ErrAdminChanged):
		return http.StatusConflict, "這個道具剛剛被玩家變動了（使用、強化或堆疊），沒有套用修改。已載入最新內容，請確認後再改一次"
	case code == "busy", errors.Is(err, domain.ErrAlreadyOnline), errors.Is(err, domain.ErrStaleOwner):
		return http.StatusConflict, "玩家正在登入、登出、換地圖、換頻道、交易或在商城裡，沒有套用修改，請稍後再試"
	case code == "invalid_request", errors.Is(err, domain.ErrInvalidInput):
		return http.StatusBadRequest, "內容不正確：數值超出範圍，或這個道具不能這樣修改"
	case code == "not_found", errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "找不到這個道具，玩家可能剛剛移動或用掉了，請重新整理"
	case code == "insufficient", errors.Is(err, domain.ErrInsufficient):
		return http.StatusConflict, "背包空間不足，或餘額不夠扣"
	case code == "forbidden", errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden, "這個道具不能從後台修改"
	}
	return http.StatusInternalServerError, "操作失敗，沒有確認到結果。已載入最新內容，請先確認再決定要不要重做"
}

// characterInventory applies one edit. The world is asked first; only when it
// says the character is not in it does the edit go to the database, where the
// store refuses it if any session still owns the character.
func (s *Server) characterInventory(w http.ResponseWriter, r *http.Request) {
	var in inventoryEdit
	if !readJSON(w, r, &in) {
		return
	}
	repo, ok := s.repo.(domain.AdminInventoryRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "這個資料庫不支援背包管理")
		return
	}
	req := domain.AdminInventoryRequest{OperationID: "admin-" + platform.NewID(), Actor: actor(r), CharacterID: in.CharacterID,
		Kind: in.Kind, InvType: in.InvType, ItemID: in.ItemID, Quantity: in.Quantity}
	var err error
	if in.AssetID != "" {
		if req.AssetID, err = strconv.ParseInt(in.AssetID, 10, 64); err != nil {
			writeError(w, http.StatusBadRequest, "道具編號不正確")
			return
		}
	}
	if in.Kind == domain.AdminGiveMesos {
		if req.Mesos, err = strconv.ParseInt(strings.TrimSpace(in.Amount), 10, 64); err != nil || req.Mesos == 0 {
			writeError(w, http.StatusBadRequest, "請填要給予的數量（負數是扣除）")
			return
		}
	}
	if in.Kind == domain.AdminSetEquipment {
		if in.Stats == nil {
			writeError(w, http.StatusBadRequest, "請求格式錯誤")
			return
		}
		req.Equipment = in.Stats.equipment()
	}
	switch in.Kind {
	case domain.AdminSetEquipment, domain.AdminSetQuantity, domain.AdminRemove:
		// An edit of an existing item is only applied to the item as the
		// operator saw it; the store compares under the character's lock.
		if in.Seen == nil || in.Seen.ItemID == 0 || in.InvType == 1 && in.Seen.Stats == nil {
			writeError(w, http.StatusBadRequest, "請求格式錯誤")
			return
		}
		req.Expect = &domain.AdminExpect{ItemID: in.Seen.ItemID, Quantity: in.Seen.Quantity}
		if in.Seen.Stats != nil {
			req.Expect.Equipment = in.Seen.Stats.equipment()
		}
	}
	var worldID int
	if err = s.db.QueryRowContext(r.Context(), `SELECT world_id FROM game_characters WHERE id=$1`, in.CharacterID).Scan(&worldID); err != nil {
		writeError(w, http.StatusNotFound, "找不到這個角色")
		return
	}
	var result domain.AdminInventoryResult
	code, err := s.worldCall(r.Context(), worldEdit, worldID, http.MethodPost, "/admin/characters/inventory", req, &result)
	online := err == nil && code == ""
	if err != nil || code == "not_online" {
		// A world that is down holds no character; one that is merely slow
		// still owns its sessions, and the store then refuses this.
		code = ""
		_, err = repo.ApplyAdminInventory(r.Context(), req)
	}
	if err != nil || code != "" {
		status, message := editError(err, code)
		slog.Warn("admin_inventory_failed", "actor", req.Actor, "character", req.CharacterID, "kind", req.Kind, "operation", req.OperationID, "code", code, "error", err)
		writeError(w, status, message)
		return
	}
	slog.Info("admin_inventory", "actor", req.Actor, "character", req.CharacterID, "kind", req.Kind, "item", req.ItemID, "asset", req.AssetID,
		"quantity", req.Quantity, "mesos", req.Mesos, "online", online, "operation", req.OperationID)
	s.writeCharacter(w, r, in.CharacterID)
}

func (s *Server) characterWallet(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CharacterID int64  `json:"character_id"`
		Currency    string `json:"currency"`
		Amount      string `json:"amount"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	repo, ok := s.repo.(domain.AdminInventoryRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "這個資料庫不支援背包管理")
		return
	}
	req := domain.AdminWalletRequest{OperationID: "admin-" + platform.NewID(), Actor: actor(r), CharacterID: in.CharacterID}
	switch in.Currency {
	case "gash":
		req.Currency = domain.CashCurrencyCredit
	case "points":
		req.Currency = domain.CashCurrencyPoints
	default:
		writeError(w, http.StatusBadRequest, "請求格式錯誤")
		return
	}
	var err error
	if req.Delta, err = strconv.ParseInt(strings.TrimSpace(in.Amount), 10, 64); err != nil || req.Delta == 0 {
		writeError(w, http.StatusBadRequest, "請填要給予的數量（負數是扣除）")
		return
	}
	if err = s.db.QueryRowContext(r.Context(), `SELECT account_id FROM game_characters WHERE id=$1`, in.CharacterID).Scan(&req.AccountID); err != nil {
		writeError(w, http.StatusNotFound, "找不到這個角色")
		return
	}
	if _, err = repo.AdjustAdminWallet(r.Context(), req); err != nil {
		status, message := editError(err, "")
		slog.Warn("admin_wallet_failed", "actor", req.Actor, "account", req.AccountID, "currency", in.Currency, "operation", req.OperationID, "error", err)
		writeError(w, status, message)
		return
	}
	slog.Info("admin_wallet", "actor", req.Actor, "account", req.AccountID, "currency", in.Currency, "delta", req.Delta, "operation", req.OperationID)
	s.writeCharacter(w, r, in.CharacterID)
}

// itemSearch finds items to give, by name or by the start of an item ID.
func (s *Server) itemSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) > 40 {
		writeError(w, http.StatusBadRequest, "搜尋文字太長")
		return
	}
	found, err := content.SearchItems(wzRoot(), query, 60)
	if err != nil {
		slog.Warn("admin_item_search", "error", err)
	}
	out := []itemMeta{}
	for _, text := range found {
		// Only what the server itself can create is offered.
		if m := metaOf(text.ID); m.Known {
			out = append(out, m)
		}
	}
	if id, e := strconv.Atoi(query); e == nil && len(out) == 0 {
		// An item without a String.wz name can still be given by its ID.
		if m := metaOf(id); m.Known {
			out = append(out, m)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "named": wzRoot() != ""})
}

// iconStore serves bag icons out of the game client's .wz files. Without
// them the page falls back to showing item names.
type iconStore struct {
	root     string
	mu       sync.Mutex
	archives map[string]*wz.Archive
	cache    sync.Map // item ID -> PNG bytes; nil for an item with no icon
}

func (s *Server) icons() *iconStore {
	s.iconsOnce.Do(func() {
		root := strings.TrimSpace(os.Getenv("MAPLEGO_CLIENT_WZ_ROOT"))
		if root == "" {
			// tools/local.ps1 keeps a copy of the client here.
			if _, err := os.Stat(filepath.Join(".local", "client", "Item.wz")); err == nil {
				root = filepath.Join(".local", "client")
			}
		}
		s.iconStore = &iconStore{root: root, archives: map[string]*wz.Archive{}}
	})
	return s.iconStore
}

func (i *iconStore) available() bool { return i.root != "" }

func (i *iconStore) png(id int) []byte {
	if cached, ok := i.cache.Load(id); ok {
		return cached.([]byte)
	}
	name, path, ok := content.ItemIconPath(id)
	if !ok || i.root == "" {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	archive, opened := i.archives[name]
	if !opened {
		var err error
		if archive, err = wz.Open(filepath.Join(i.root, name)); err != nil {
			slog.Warn("admin_icon_archive", "archive", name, "error", err)
			archive = nil
		}
		i.archives[name] = archive
	}
	var out []byte
	if archive != nil {
		if img, err := archive.Canvas(path); err == nil {
			var buffer bytes.Buffer
			if png.Encode(&buffer, img) == nil {
				out = buffer.Bytes()
			}
		} else if !errors.Is(err, wz.ErrNotFound) {
			slog.Debug("admin_icon", "item", id, "error", err)
		}
	}
	i.cache.Store(id, out)
	return out
}

func (s *Server) itemIcon(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.URL.Query().Get("id"))
	if err != nil || id < 1000000 || id > 5999999 {
		http.NotFound(w, r)
		return
	}
	data := s.icons().png(id)
	if data == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data)
}
