// Command loadbot is a headless v145 protocol load client.
//
// It reuses internal/protocol Dial (client AES / framing) and is meant to
// grow into concurrent login→channel scenarios. This milestone only proves
// the binary builds and can complete the version handshake against a live
// login listener.
//
// Next steps (see also cmd/smoke and docs in the project store):
//  1. RecvLogin → world list / character list
//  2. RecvSelect → SendServerIP handoff (opaque handle)
//  3. Re-Dial channel port → RecvLoggedIn → SendSetField
//  4. Scenario scripts: move, skill, combat, map/channel change, logout
//
// Credentials must come from flags/env or ignored .local files — never commit
// production account secrets into the repository.
package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"time"

	"maple-go-server/internal/protocol"
)

func main() {
	loginAddr := flag.String("login", "127.0.0.1:18484", "login TCP address")
	timeout := flag.Duration("timeout", 10*time.Second, "dial and handshake deadline")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	c, err := protocol.Dial(ctx, *loginAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loadbot: dial %s: %v\n", *loginAddr, err)
		os.Exit(1)
	}
	defer c.Close()
	c.SetTimeouts(*timeout, *timeout)

	// Version exchange — same shape as cmd/smoke dial().
	w := protocol.NewWriter(protocol.RecvHello)
	w.U8(6)
	w.U16(protocol.Version)
	w.U16(1)
	if err := w.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "loadbot: hello writer: %v\n", err)
		os.Exit(1)
	}
	if err := c.WritePacket(w.Packet()); err != nil {
		fmt.Fprintf(os.Stderr, "loadbot: write hello: %v\n", err)
		os.Exit(1)
	}
	p, err := c.ReadPacket()
	// The launcher settings come first; the bot has no use for them.
	for err == nil && len(p) >= 2 && (binary.LittleEndian.Uint16(p[:2]) == protocol.SendClientConfig || binary.LittleEndian.Uint16(p[:2]) == protocol.SendClientUI) {
		p, err = c.ReadPacket()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "loadbot: read after hello: %v\n", err)
		os.Exit(1)
	}
	if len(p) < 2 || binary.LittleEndian.Uint16(p[:2]) != protocol.SendShowMapleStory {
		fmt.Fprintf(os.Stderr, "loadbot: unexpected opcode after hello (len=%d)\n", len(p))
		os.Exit(1)
	}

	fmt.Println("loadbot: OK handshake with", *loginAddr)
	fmt.Println("loadbot: stub only — wire RecvLogin / select / world entry next (mirror cmd/smoke)")
}
