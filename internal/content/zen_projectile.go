package content

import "encoding/json"

type projectileSeed struct {
	ItemID int `json:"itemId"`
	Attack int `json:"incPAD"`
}

var zenProjectiles = func() map[int]projectileSeed {
	data, err := v148Supplement.ReadFile("v148/zen-projectiles.json")
	if err != nil {
		panic(err)
	}
	var projectiles map[int]projectileSeed
	if err := json.Unmarshal(data, &projectiles); err != nil {
		panic(err)
	}
	return projectiles
}()

// ZenProjectile returns the advancement's built-in projectile ID and attack.
func ZenProjectile(job int) (itemID, attack int) {
	entry := zenProjectiles[job]
	return entry.ItemID, entry.Attack
}
