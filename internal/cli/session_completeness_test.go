package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestViewerIgnoresStaleLoads(t *testing.T) {
	m := newBrowserModel([]Session{{ID: "session-A"}, {ID: "session-B"}}, false)
	_, _ = m.openViewer()
	first := m.viewerRequest
	m.closeViewer()
	m.list.Select(1)
	_, _ = m.openViewer()
	second := m.viewerRequest
	_, _ = m.handleLoaded(loadedContentMsg{id: "session-B", request: second, messages: []proto.Message{{Role: proto.RoleUser, Content: "current B"}}})
	for _, err := range []error{nil, errors.New("old error")} {
		_, _ = m.handleLoaded(loadedContentMsg{id: "session-A", request: first, err: err, messages: []proto.Message{{Content: "old A"}}})
		require.Equal(t, "current B", m.viewerDoc.content)
		require.Empty(t, m.viewErr)
	}
	m.closeViewer()
	_, _ = m.handleLoaded(loadedContentMsg{id: "session-B", request: second})
	require.False(t, m.viewerLoaded)
	_, _ = m.openViewer()
	require.Greater(t, m.viewerRequest, second)
	_, _ = m.handleLoaded(loadedContentMsg{id: "session-B", request: second, err: errors.New("same session old error")})
	require.False(t, m.viewerLoaded)
	require.Empty(t, m.viewErr)
	_, _ = m.handleLoaded(loadedContentMsg{id: "session-B", request: m.viewerRequest, messages: []proto.Message{{Content: "new B"}}})
	require.Equal(t, "new B", m.viewerDoc.content)
}

func TestViewerLoadsSavedAttachmentsAndApprovalCount(t *testing.T) {
	d := testDB(t)
	messages := []proto.Message{
		{Role: proto.RoleUser, Images: []proto.Image{{MimeType: "image/png", Data: []byte{1, 2, 3}}}},
		{Role: proto.RoleAssistant, ProviderData: map[string]json.RawMessage{"state": json.RawMessage(`{"private":"opaque"}`)}},
		{Role: proto.RoleAssistant},
	}
	require.NoError(t, d.SaveSession("session-A", "attachments", "api", "model", messages, []approval.Rule{{Type: approval.DirAllow, Paths: []string{"/tmp"}, Mode: approval.AccessWrite}}))
	m := newBrowserModel([]Session{{ID: "session-A", Title: "attachments"}}, false)
	m.db = d
	_, cmd := m.openViewer()
	require.NotNil(t, cmd)
	_, _ = m.Update(cmd())
	require.Empty(t, m.viewErr)
	require.True(t, m.viewerLoaded)
	require.Equal(t, messages, m.viewerMsgs)
	require.Equal(t, 1, m.viewerApprovalCount)
	require.Contains(t, ansi.Strip(m.viewerHeader), "3 messages · 1 saved approval rules")
	require.Contains(t, m.viewerDoc.content, "images (1):\n- 1: image/png, 3 bytes (not displayed)")
	require.Contains(t, m.viewerDoc.content, "Internal continuation data: 1 fields (not expanded)")
	require.Contains(t, m.viewerDoc.content, "(empty message)")
	require.NotContains(t, m.viewerDoc.content, "opaque")
	starts := 0
	for _, line := range m.viewerDoc.lines {
		if line.start {
			starts++
		}
	}
	require.Equal(t, 3, starts)
	require.NotContains(t, m.viewViewer(), "This session has no messages")
}

func TestViewerCopyMatchesAllReadableMessages(t *testing.T) {
	messages := []proto.Message{
		{Role: proto.RoleUser, Content: strings.Repeat("long text\n", 40), Images: []proto.Image{{Data: []byte{1}}}},
		{Role: proto.RoleAssistant, ToolCalls: []proto.ToolCall{{Function: proto.Function{Name: "shell_run", Arguments: []byte(`{"command":"pwd","cwd":"/tmp","id":9007199254740993}`)}}}},
		{Role: proto.RoleTool, ToolCalls: []proto.ToolCall{{Function: proto.Function{Name: "shell_run"}}}},
		{Role: proto.RoleAssistant, ProviderData: map[string]json.RawMessage{"state": json.RawMessage(`"private"`)}},
	}
	m := newLoadedViewer(t, 80, 12, messages)
	m.viewerApprovalCount = 2
	saved := copyTextToClipboard
	t.Cleanup(func() { copyTextToClipboard = saved })
	var copied string
	copyTextToClipboard = func(s string) { copied = s }
	m.viewport.SetYOffset(15)
	m.toggleViewerWrap()
	m.applyViewerSearch("pwd")
	sendViewerKey(m, 'C', "C")
	require.True(t, strings.HasPrefix(copied, "mods session\n"))
	require.Contains(t, copied, "approval_rules: 2 (not expanded)")
	require.NotContains(t, copied, "mods session debug")
	for _, msg := range messages {
		content := readableMessageContent(msg)
		require.Contains(t, m.viewerDoc.content, content)
		require.Contains(t, copied, content)
	}
	require.Contains(t, copied, "9007199254740993")
	require.NotContains(t, copied, "private")
}

func TestViewerHeaderFitsAfterResize(t *testing.T) {
	m := newBrowserModel([]Session{{ID: "df31ae23ab8b75b5643c2f846c570997edc71333", Title: "a long session title with 中文内容"}}, false)
	_, _ = m.openViewer()
	_, _ = m.handleLoaded(loadedContentMsg{id: m.viewing.conv.ID, request: m.viewerRequest, approvalCount: 3, messages: []proto.Message{{Role: proto.RoleUser, Content: "hello"}}})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
	for _, line := range strings.Split(m.viewerHeader, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(line), 40)
	}
	require.Equal(t, 24-lipgloss.Height(m.viewerHeader)-browserFooterHeight, m.viewport.Height())
	require.Contains(t, ansi.Strip(m.View().Content), "hello")
}

func TestReadableMessageMarksOpaqueToolSignatures(t *testing.T) {
	content := readableMessageContent(proto.Message{Role: proto.RoleAssistant, ToolCalls: []proto.ToolCall{{Function: proto.Function{Name: "lookup", ThoughtSignature: "opaque-signature"}}}})
	require.Contains(t, content, "Internal continuation data: 1 fields (not expanded)")
	require.NotContains(t, content, "opaque-signature")
}
