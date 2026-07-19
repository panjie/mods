package proto

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContextClassIsRequestLocalAndNotSerialized(t *testing.T) {
	message := Message{Role: RoleUser, Content: "hello"}
	message.SetContextClass(ContextClassCurrentUser)

	jsonData, err := json.Marshal(message)
	require.NoError(t, err)
	require.NotContains(t, string(jsonData), "contextClass")

	var encoded bytes.Buffer
	require.NoError(t, gob.NewEncoder(&encoded).Encode(message))
	var decoded Message
	require.NoError(t, gob.NewDecoder(&encoded).Decode(&decoded))
	require.Equal(t, ContextClassUnspecified, decoded.ContextClass())
	require.Equal(t, "hello", decoded.Content)
}
