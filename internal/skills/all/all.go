// Package all imports every job package so their skills are registered.
// Import it for side effects wherever the registry is consulted.
package all

import (
	_ "maple-go-server/internal/skills/aranevan"
	_ "maple-go-server/internal/skills/bowman"
	_ "maple-go-server/internal/skills/common"
	_ "maple-go-server/internal/skills/cygnus"
	_ "maple-go-server/internal/skills/magician"
	_ "maple-go-server/internal/skills/mercphantom"
	_ "maple-go-server/internal/skills/pirate"
	_ "maple-go-server/internal/skills/resist1"
	_ "maple-go-server/internal/skills/resist2"
	_ "maple-go-server/internal/skills/thief"
	_ "maple-go-server/internal/skills/warrior"
)
