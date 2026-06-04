package main

import (
	"crypto/rand"
	"crypto/sha1" //nolint: gosec
	"fmt"
	"regexp"
)

const (
	sha1short         = 7
	sha1minLen        = 4
	sha1ReadBlockSize = 4096
)

var sha1reg = regexp.MustCompile(`\b[0-9a-f]{40}\b`)

func newConversationID() string {
	b := make([]byte, sha1ReadBlockSize)
	if _, err := rand.Read(b); err != nil {
		debugPrintf("rand.Read failed for conversation ID: %v", err)
	}
	return fmt.Sprintf("%x", sha1.Sum(b)) //nolint: gosec
}
