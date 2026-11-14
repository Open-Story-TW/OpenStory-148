// Command build-mob-bodies records deterministic visual envelopes for every
// embedded monster. It only reads WZ XML and never modifies the source files.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

	"maple-go-server/internal/content"
)

func main() {
	wz := flag.String("wz", "", "read-only WZ XML root")
	starter := flag.String("starter", "internal/content/starter.json", "embedded starter content")
	out := flag.String("out", "internal/content/mob_body_seed.json", "output body seed")
	flag.Parse()
	if err := build(*wz, *starter, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func build(root, starterPath, outputPath string) error {
	if root == "" {
		return fmt.Errorf("-wz is required")
	}
	raw, err := os.ReadFile(starterPath)
	if err != nil {
		return err
	}
	var starter struct {
		Mobs []content.Mob `json:"mobs"`
	}
	if err := json.Unmarshal(raw, &starter); err != nil {
		return err
	}
	if len(starter.Mobs) == 0 {
		return fmt.Errorf("starter has no monsters")
	}
	sort.Slice(starter.Mobs, func(i, j int) bool { return starter.Mobs[i].ID < starter.Mobs[j].ID })
	hash := sha256.Sum256(raw)
	seed := content.MobBodySeed{Version: 1, StarterSHA256: hex.EncodeToString(hash[:])}
	for i, mob := range starter.Mobs {
		if i > 0 && starter.Mobs[i-1].ID == mob.ID {
			return fmt.Errorf("duplicate starter monster %d", mob.ID)
		}
		bounds, err := content.MobBodyDefinition(root, mob.ID)
		if err != nil {
			return fmt.Errorf("monster %d: %w", mob.ID, err)
		}
		seed.Definitions = append(seed.Definitions, content.MobBodySeedEntry{ID: mob.ID, Bounds: bounds})
	}
	data, err := json.MarshalIndent(seed, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(outputPath, append(data, '\n'), 0644); err != nil {
		return err
	}
	fmt.Printf("%d visual envelopes -> %s\n", len(seed.Definitions), outputPath)
	return nil
}
