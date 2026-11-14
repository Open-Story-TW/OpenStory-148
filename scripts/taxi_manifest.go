package scripts

import (
	_ "embed"
	"encoding/json"
)

//go:embed taxi-npcs.json
var taxiMigrationData []byte

type TaxiMigration struct {
	Legacy       string `json:"legacy"`
	SourceSHA256 string `json:"source_sha256"`
	Lua          string `json:"lua"`
	LuaSHA256    string `json:"lua_sha256"`
	NPCID        int    `json:"npc_id"`
	SourceMapID  int    `json:"source_map_id"`
	Scope        string `json:"scope"`
	NativeMCP    string `json:"native_mcp"`
}

func TaxiMigrations() ([]TaxiMigration, error) {
	var m struct {
		Scripts []TaxiMigration `json:"scripts"`
	}
	err := json.Unmarshal(taxiMigrationData, &m)
	return m.Scripts, err
}
