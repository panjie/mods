package conversation

import (
	"crypto/rand"
	"crypto/sha1" //nolint: gosec
	"fmt"
	mathrand "math/rand/v2"
	"regexp"
	"time"

	"github.com/charmbracelet/mods/internal/debug"
)

const (
	ShortIDLength     = 7
	MinIDLength       = 4
	sha1ReadBlockSize = 4096
)

var IDPattern = regexp.MustCompile(`\b[0-9a-f]{40}\b`)

func NewID() string {
	b := make([]byte, sha1ReadBlockSize)
	if _, err := rand.Read(b); err != nil {
		debug.Printf("rand.Read failed for conversation ID: %v, falling back to math/rand", err)
		// Fall back to math/rand seeded with time, so conversation IDs remain
		// unique even when the system CSPRNG is unavailable.
		rng := mathrand.New(mathrand.NewPCG(uint64(time.Now().UnixNano()), 0))
		for i := range b {
			b[i] = byte(rng.UintN(256))
		}
	}
	return fmt.Sprintf("%x", sha1.Sum(b)) //nolint: gosec
}
