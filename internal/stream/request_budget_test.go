package stream

import (
	"context"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestRequestBudgetSharedAcrossConversations(t *testing.T) {
	ctx := WithRequestBudget(context.Background(), 1)
	calls := 0
	factory := func(context.Context, proto.Request) Round { calls++; return Failed(nil, nil) }
	one := NewConversation(ctx, proto.Request{}, factory)
	require.False(t, one.Next())
	require.NoError(t, one.Err())
	two := NewConversation(ctx, proto.Request{}, factory)
	require.False(t, two.Next())
	require.ErrorContains(t, two.Err(), "budget exhausted")
	require.Equal(t, 1, calls)
}
