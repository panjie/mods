package conversation

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"io"

	"github.com/panjie/mods/internal/proto"
)

func encodeConversation(messages []proto.Message) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(messages); err != nil {
		return nil, fmt.Errorf("encode conversation: %w", err)
	}
	return buf.Bytes(), nil
}

func decodeConversationBytes(data []byte, messages *[]proto.Message) error {
	return decodeConversation(bytes.NewReader(data), messages)
}

func decodeConversation(r io.Reader, messages *[]proto.Message) error {
	var backup bytes.Buffer
	if err1 := gob.NewDecoder(io.TeeReader(r, &backup)).Decode(messages); err1 != nil {
		var legacy []legacyMessage
		if err2 := gob.NewDecoder(&backup).Decode(&legacy); err2 != nil {
			return fmt.Errorf("decode conversation: %w", err1)
		}
		for _, msg := range legacy {
			*messages = append(*messages, proto.Message{
				Role:    msg.Role,
				Content: msg.Content,
			})
		}
	}
	return nil
}

// legacyMessage matches the pre-tool-call gob shape.
type legacyMessage struct {
	Content string
	Role    string
}
