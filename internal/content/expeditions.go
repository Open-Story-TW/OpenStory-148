package content

// ExpeditionType is one v148 Quest.wz PQuestSearch.img/expedition entry. The
// client numbers them 2000 + the node name (0x65AF8C loads the list with base
// 0x7D0) and sends that id when its expedition tab creates one; its
// invitation window looks the id up and fails on an unknown one.
type ExpeditionType struct {
	ID       int
	Name     string
	LevelMin int
	LevelMax int
	Users    int
}

// MaxParties is how many six-member parties the expedition may hold; the
// v148 status packet carries at most five.
func (t ExpeditionType) MaxParties() int {
	return min(5, max(1, (t.Users+5)/6))
}

// The v148 list differs from the v145 data root (no 2001, 希拉 is new), so it
// is copied from the table the v148 client loads (id, name, levelMin,
// levelMax, userCount), read back from a running client.
var expeditionTypes = map[int]ExpeditionType{
	2000: {2000, "巴洛古遠征隊 [簡易版]", 45, 200, 6},
	2002: {2002, "殘暴炎魔遠征隊", 50, 200, 30},
	2003: {2003, "闇黑龍王遠征隊", 80, 200, 30},
	2004: {2004, "皮卡啾遠征隊", 120, 200, 30},
	2005: {2005, "混沌殘暴炎魔遠征隊", 100, 200, 30},
	2006: {2006, "混沌闇黑龍王遠征隊", 110, 200, 30},
	2007: {2007, "凡雷恩遠征隊", 120, 200, 18},
	2008: {2008, "西格諾斯遠征隊", 170, 200, 18},
	2009: {2009, "阿卡伊農遠征隊", 120, 200, 18},
	2010: {2010, "希拉遠征隊", 120, 200, 6},
}

func ExpeditionTypeByID(id int) (ExpeditionType, bool) {
	t, ok := expeditionTypes[id]
	return t, ok
}
