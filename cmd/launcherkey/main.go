// Command launcherkey creates the key pair of the launcher key exchange.
// The private line goes into MAPLEGO_LAUNCHER_KEY on every game service; the
// public line is compiled into the launcher (OPENMAPLE_SERVER_KEYS).
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"

	"maple-go-server/internal/protocol"
)

func main() {
	show := flag.Bool("public", false, "print the public key of "+protocol.LauncherKeyEnv+" instead of creating a key")
	flag.Parse()
	if *show {
		private, err := protocol.ParseLauncherKey(os.Getenv(protocol.LauncherKeyEnv))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(hex.EncodeToString(private.Public().(ed25519.PublicKey)))
		return
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("private (server, keep secret): " + base64.StdEncoding.EncodeToString(private.Seed()))
	fmt.Println("public  (launcher build):      " + hex.EncodeToString(public))
}
