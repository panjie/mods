package cli

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"testing"

	tea "charm.land/bubbletea/v2"
	cfgpkg "github.com/panjie/mods/internal/config"
	imageutil "github.com/panjie/mods/internal/image"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestChatClipboardPasteSnapshotAndRemoval(t *testing.T) {
	oldImage, oldText := chatReadImage, chatReadText
	t.Cleanup(func() { chatReadImage, chatReadText = oldImage, oldText })
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	data := buf.Bytes()
	chatReadImage = func() ([]byte, string, error) { return data, "ignored", nil }
	chatReadText = func() (string, error) { t.Fatal("image must take precedence"); return "", nil }
	m := newChatPromptModel(nil)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	m = updated.(chatPromptModel)
	require.True(t, m.pasting)
	require.NotNil(t, cmd)
	blocked, _ := m.submit()
	require.False(t, blocked.(chatPromptModel).done)
	m = updateChatPrompt(t, m, cmd())
	require.False(t, m.pasting)
	require.Len(t, m.media, 1)
	require.Equal(t, "image/png", m.media[0].MimeType)
	require.Contains(t, m.View().Content, "clipboard image 1")
	original := append([]byte(nil), data...)
	data[0] = 0
	require.Equal(t, original, m.media[0].Data)
	sent, _ := m.submit()
	require.True(t, sent.(chatPromptModel).done, "image-only submission")
	m = updateChatPrompt(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace, Mod: tea.ModAlt})
	require.Empty(t, m.media)
	blank, _ := m.submit()
	require.False(t, blank.(chatPromptModel).done)
}

func TestChatClipboardAltVPaste(t *testing.T) {
	oldImage, oldText := chatReadImage, chatReadText
	t.Cleanup(func() { chatReadImage, chatReadText = oldImage, oldText })
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	chatReadImage = func() ([]byte, string, error) { return buf.Bytes(), "image/png", nil }
	chatReadText = func() (string, error) { t.Fatal("image must take precedence"); return "", nil }
	m := newChatPromptModel(nil)
	m.textarea.SetValue("describe this")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModAlt})
	m = updated.(chatPromptModel)
	require.True(t, m.pasting)
	require.NotNil(t, cmd)
	for _, mod := range []tea.KeyMod{tea.ModAlt, tea.ModCtrl} {
		_, duplicate := m.Update(tea.KeyPressMsg{Code: 'v', Mod: mod})
		require.Nil(t, duplicate, "ignore repeated paste while clipboard read is pending")
	}
	m = updateChatPrompt(t, m, cmd())
	require.False(t, m.pasting)
	require.Len(t, m.media, 1)
	require.Equal(t, buf.Bytes(), m.media[0].Data)
	require.Equal(t, "describe this", m.textarea.Value())
	require.Contains(t, m.View().Content, "Alt+V")
}

func TestChatClipboardTextAndErrorsPreserveDraft(t *testing.T) {
	oldImage, oldText := chatReadImage, chatReadText
	t.Cleanup(func() { chatReadImage, chatReadText = oldImage, oldText })
	chatReadImage = func() ([]byte, string, error) { return nil, "", errors.New("no image") }
	chatReadText = func() (string, error) { return "hello\nworld", nil }
	m := updateChatPrompt(t, newChatPromptModel(nil), readChatClipboard())
	require.Equal(t, "hello\nworld", m.textarea.Value())
	require.Empty(t, m.media)
	chatReadText = func() (string, error) { return "", errors.New("unavailable") }
	m = updateChatPrompt(t, m, readChatClipboard())
	require.Equal(t, "hello\nworld", m.textarea.Value())
	require.NotEmpty(t, m.pasteError)
	chatReadImage = func() ([]byte, string, error) { return []byte("invalid"), "", nil }
	require.Error(t, readChatClipboard().(chatClipboardMsg).err)
}

func TestChatClipboardAttachmentLimits(t *testing.T) {
	m := newChatPromptModel(nil)
	m.media = make([]proto.MediaAttachment, imageutil.MaxImageCount)
	m = updateChatPrompt(t, m, chatClipboardMsg{data: []byte{1}, mime: "image/png"})
	require.Len(t, m.media, imageutil.MaxImageCount)
	require.NotEmpty(t, m.pasteError)
	m = newChatPromptModel(nil)
	m.media = []proto.MediaAttachment{{Data: make([]byte, imageutil.MaxTotalImageBytes)}}
	m = updateChatPrompt(t, m, chatClipboardMsg{data: []byte{1}, mime: "image/png"})
	require.Len(t, m.media, 1)
	require.NotEmpty(t, m.pasteError)
}

func TestChatMediaFlowsThroughTurnsWithoutReattaching(t *testing.T) {
	withChatTest(t, "", func(_ *[]string) {
		IsErrorTTY = func() bool { return true }
		media := []proto.MediaAttachment{{Kind: "image", Data: []byte{1, 2, 3}, MimeType: "image/png", Source: "clipboard image 1"}}
		inputs := []chatPromptResult{{Media: media}, {Prompt: "follow up"}}
		chatPromptInput = func([]string) (chatPromptResult, bool, error) {
			if len(inputs) == 0 {
				return chatPromptResult{}, true, nil
			}
			input := inputs[0]
			inputs = inputs[1:]
			return input, false, nil
		}
		turn := 0
		chatTurn = func(ctx context.Context, prompt string, _ []tea.ProgramOption) (*Mods, error) {
			cfg := turnConfig(ctx)
			if turn == 0 {
				require.Empty(t, prompt)
				require.Equal(t, media, cfg.Media)
				cfg.Media[0].Data[0] = 9
				require.Equal(t, byte(1), media[0].Data[0])
			} else {
				require.Empty(t, cfg.Media)
				require.Equal(t, "follow up", prompt)
			}
			require.Empty(t, config.Media)
			turn++
			cfg.SessionWriteToID = "df31ae23ab8b75b5643c2f846c570997edc71333"
			return &Mods{Config: cfg}, nil
		}
		require.NoError(t, runChat(context.Background(), nil, nil))
		require.Equal(t, 2, turn)
	})
}

func TestChatInitialMediaAndPastedMediaAreIsolated(t *testing.T) {
	cfg := Config{Media: []proto.MediaAttachment{{Kind: "image", Data: []byte{1}}}}
	s := chatSession{options: cfgpkg.NewInvocationOptions(cfg), initial: cfg.InitialInput()}
	cfg.Media[0].Data[0] = 9
	ctx := s.next(context.Background(), chatPromptResult{Media: []proto.MediaAttachment{{Kind: "image", Data: []byte{2}}}})
	got := turnConfig(ctx)
	require.Len(t, got.Media, 2)
	require.Equal(t, byte(1), got.Media[0].Data[0])
	got.Media[0].Data[0] = 8
	require.Equal(t, byte(1), s.initial.Media[0].Data[0])
	s.started = true
	require.Empty(t, turnConfig(s.next(context.Background(), chatPromptResult{Prompt: "next"})).Media)
}
