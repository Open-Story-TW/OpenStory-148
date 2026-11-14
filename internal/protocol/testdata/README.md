# Protocol fixture provenance

`crypto_vectors.json` was generated on 2026-10-31 by a standalone Java oracle
compiled into the Go project's `.local/protocol-java-oracle` directory. It copied
`MapleStory_Legacy_Server/src/tools/MapleAESOFB.java` read-only (SHA256
`B4F0EC3984790C8853758B3898B8F6C30DBDF64F348422D65392CEA9B9F9D3B7`), replacing
only its configuration-selected key expression with its normal SKEY. Small local
BitTools/HexTool stubs removed application initialization dependencies. Neither
the running Java server nor its database was used or modified.

Each input starts with IV `4d23c72b`, protocol version 145, and byte `i*31+7`
at offset i. Boundary lengths exercise the initial 0x5b0 block, later 0x5b4
blocks, and maximum 16-bit payload. Fixtures cover frame header, encrypted
payload SHA256 and next IV, independently of the Go encoder/decoder pair.

The Go tests also exercise real TCP fragmentation, IV advancement, malformed
headers, Big5 encoding, and bounded reads. Full gameplay and GUI verification
are recorded separately by the root integration work; they are not implied by
these codec tests.

`starter_packets.json` independently records `addCharStats` and `addCharLook`
from Java `PacketHelper.java` SHA256
`184F3FC70C30D38D36D7951A5A2252D0D0AA275A6B73628AA5B56549C97F7363`.
The original method bodies and original Big5 writer were compiled into a
standalone `.local/protocol-java-packets` harness with detached starter getters,
level32 enabled, and fixed time 1793448000000 ms (2026-10-31 20:00 UTC+8).
No application initializer or database was used. Go compares the complete
stats/look byte sequences against this Java output.
