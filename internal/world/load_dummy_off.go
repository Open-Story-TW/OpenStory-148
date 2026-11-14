//go:build !loaddummy

package world

import "context"

// startLoadDummies is a no-op unless the binary is built with -tags loaddummy.
//
// Injection points for real dummies (when enabled):
//   - Server.online — channel-scoped presence counted by Status / health
//   - future per-map player / actor lists (see docs/ARCHITECTURE.md)
func (s *Server) startLoadDummies(context.Context) {}
