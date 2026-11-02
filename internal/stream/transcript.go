package stream

import (
	"context"
	"sync"

	"github.com/panjie/mods/internal/proto"
)

// Transcript is the committed conversation view. Only complete assistant/tool
// groups are published. Snapshots never share mutable bytes with providers.
type Transcript struct {
	mu       sync.RWMutex
	messages []proto.Message
}

func (t *Transcript) Replace(messages []proto.Message) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.messages = proto.CloneMessages(messages)
}
func (t *Transcript) Snapshot() []proto.Message {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return proto.CloneMessages(t.messages)
}

type transcriptKey struct{}

func WithTranscript(ctx context.Context, t *Transcript) context.Context {
	return context.WithValue(ctx, transcriptKey{}, t)
}
