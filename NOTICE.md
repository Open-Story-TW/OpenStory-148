# Source provenance

The Legacy-compatible protocol portions in `internal/protocol` are derived from
the MapleStory Legacy / OdinMS Java source used as the local reference.
Original source notices identify Copyright (C) 2008-2010 Patrick Huy,
Matthias Butz and Jan Christian Meyer and the GNU Affero General Public License
version 3. Those notices are retained in the derived protocol source files.

The AGPL version 3 license text is provided in `LICENSE`.

The baseline source paths/checksums are recorded in `docs/baseline/` and the
independent protocol test fixture provenance. Reference Java compiler harnesses
and client WZ/binaries are local inputs, not part of the Go source deliverable.
Third-party Go modules and generator versions are recorded in `go.mod` and
`go.sum`; their own licenses and notices continue to apply.
