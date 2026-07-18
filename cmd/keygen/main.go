// Command keygen generates the Ed25519 snapshot signing keypair. The seed is
// the builder's secret (CNIP_SNAPSHOT_SIGNING_KEY); the public key is pinned
// by workers to verify snapshot manifests.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
)

func main() {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, "keygen:", err)
		os.Exit(1)
	}
	fmt.Printf("CNIP_SNAPSHOT_SIGNING_KEY=%s\n", base64.StdEncoding.EncodeToString(priv.Seed()))
	fmt.Printf("CNIP_SNAPSHOT_PUBLIC_KEY=%s\n", base64.StdEncoding.EncodeToString(pub))
}
