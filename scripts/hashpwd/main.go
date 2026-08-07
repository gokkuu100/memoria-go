// Standalone argon2id hasher for seed scripts (no package imports beyond stdlib + crypto).
package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: hashpwd <password>")
		os.Exit(1)
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	key := argon2.IDKey([]byte(os.Args[1]), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	fmt.Printf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}
