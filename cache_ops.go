package main

import (
	"bufio"
	"errors"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/mods/internal/proto"
	"github.com/charmbracelet/x/exp/ordered"
)

func (m *Mods) findCacheOpsDetails() tea.Cmd {
	return func() tea.Msg {
		continueLast := m.Config.ContinueLast || (m.Config.Continue != "" && m.Config.Title == "")
		readID := ordered.First(m.Config.Continue, m.Config.Show)
		writeID := ordered.First(m.Config.Title, m.Config.Continue)
		title := writeID
		model := m.Config.Model
		api := m.Config.API
		var rules []ApprovalRule

		if readID != "" || continueLast || m.Config.ShowLast {
			found, err := m.findReadID(readID)
			if err != nil {
				return modsError{
					err:    err,
					reason: "Could not find the conversation.",
				}
			}
			if found != nil {
				readID = found.ID
				if found.Model != nil && found.API != nil {
					model = *found.Model
					api = *found.API
				}
				if !m.Config.NoCache {
					rules, err = m.db.ApprovalRules(readID)
					if err != nil {
						return modsError{
							err:    err,
							reason: "Could not load conversation approval rules.",
						}
					}
				}
			}
		}

		if continueLast {
			writeID = readID
		}

		if writeID == "" {
			writeID = newConversationID()
		}

		if !sha1reg.MatchString(writeID) {
			convo, err := m.db.Find(writeID)
			if err != nil {
				writeID = newConversationID()
			} else {
				writeID = convo.ID
			}
		}

		debugPrintf("Conversation: write_id=%s, read_id=%s, continue_last=%v, title=%s",
			writeID[:min(sha1short, len(writeID))], readID[:min(sha1short, len(readID))], continueLast, title)

		return cacheDetailsMsg{
			WriteID: writeID,
			Title:   title,
			ReadID:  readID,
			API:     api,
			Model:   model,
			Rules:   rules,
		}
	}
}

func (m *Mods) findReadID(in string) (*Conversation, error) {
	convo, err := m.db.Find(in)
	if err == nil {
		return convo, nil
	}
	if errors.Is(err, errNoMatches) && m.Config.Show == "" {
		convo, err := m.db.FindHEAD()
		if err != nil {
			return nil, err
		}
		return convo, nil
	}
	return nil, err
}

func (m *Mods) readStdinCmd() tea.Msg {
	if !isInputTTY() {
		reader := bufio.NewReader(os.Stdin)
		stdinBytes, err := io.ReadAll(reader)
		if err != nil {
			return modsError{err, "Unable to read stdin."}
		}

		debugPrintf("Stdin: pipe mode, %d bytes read", len(stdinBytes))
		debugPrintf("Stdin image mode: %v", m.Config.StdinImage)

		if m.Config.StdinImage {
			return stdinImageInput{data: stdinBytes}
		}
		return completionInput{increaseIndent(string(stdinBytes))}
	}
	debugPrintf("Stdin: TTY mode, no piped input")
	return completionInput{""}
}

func (m *Mods) readFromCache() tea.Cmd {
	return func() tea.Msg {
		var messages []proto.Message
		if err := m.db.ReadMessages(m.Config.cacheReadFromID, &messages); err != nil {
			return modsError{err, "There was an error loading the conversation."}
		}

		m.appendToOutput(proto.Conversation(messages).String())
		return completionOutput{
			errh: func(err error) tea.Msg {
				return modsError{err: err}
			},
		}
	}
}
