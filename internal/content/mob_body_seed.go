package content

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

//go:embed mob_body_seed.json
var mobBodySeed []byte

// The body seed is generated separately from combat attributes. Visual links
// must not replace the linked monster's own HP, EXP or other combat values.
type MobBodySeed struct {
	Version       int                `json:"version"`
	StarterSHA256 string             `json:"starter_sha256"`
	Definitions   []MobBodySeedEntry `json:"definitions"`
}

type MobBodySeedEntry struct {
	ID     int           `json:"id"`
	Bounds MobBodyBounds `json:"bounds"`
}

func loadEmbeddedMobBodies(data []byte, mobs map[int]Mob) (map[int]MobBodyBounds, error) {
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("embedded mob body seed exceeds 1 MiB")
	}
	var seed MobBodySeed
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&seed); err != nil {
		return nil, fmt.Errorf("embedded mob body seed: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("embedded mob body seed has trailing data")
	}
	hash := sha256.Sum256(starter)
	if seed.Version != 1 || seed.StarterSHA256 != hex.EncodeToString(hash[:]) || len(seed.Definitions) != len(mobs) {
		return nil, fmt.Errorf("embedded mob body seed does not match starter content")
	}
	bodies := make(map[int]MobBodyBounds, len(mobs))
	for _, entry := range seed.Definitions {
		if _, ok := mobs[entry.ID]; !ok {
			return nil, fmt.Errorf("unexpected embedded mob body %d", entry.ID)
		}
		if _, ok := bodies[entry.ID]; ok {
			return nil, fmt.Errorf("duplicate embedded mob body %d", entry.ID)
		}
		b := entry.Bounds
		validRect := func(r MobBodyRect) bool {
			return r.Nonempty() && r.Left >= -4096 && r.Right <= 4096 && r.Top >= -4096 && r.Bottom <= 4096
		}
		if b.Kind != "static_frame_envelope_both_facings" || b.FrameCount < 1 || b.FrameCount > 4096 || !validRect(b.Envelope) || !validRect(b.UnmirroredEnvelope) || b.Envelope != unionMobBody(b.UnmirroredEnvelope, b.UnmirroredEnvelope.Mirror()) || len(b.Sources) < 1 || len(b.Sources) > 8 || b.Sources[0].ID != entry.ID {
			return nil, fmt.Errorf("invalid embedded mob body %d", entry.ID)
		}
		seen := make(map[int]bool, len(b.Sources))
		for _, source := range b.Sources {
			rawHash, err := hex.DecodeString(source.SHA256)
			if source.ID < 1 || source.ID > 9999999 || seen[source.ID] || err != nil || len(rawHash) != sha256.Size || hex.EncodeToString(rawHash) != source.SHA256 {
				return nil, fmt.Errorf("invalid embedded mob body source for %d", entry.ID)
			}
			seen[source.ID] = true
		}
		bodies[entry.ID] = cloneMobBodyBounds(b)
	}
	return bodies, nil
}
