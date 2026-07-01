package app

import (
	"bufio"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/ordered"
	"github.com/panjie/mods/internal/proto"
)

func (m *Mods) findCacheOpsDetails() tea.Cmd {
	return func() tea.Msg {
		continueLast := m.Config.ContinueLast || (m.Config.Continue != "" && m.Config.Title == "")
		readID := ordered.First(m.Config.Continue, m.Config.Show)
		writeID := ordered.First(m.Config.Title, m.Config.Continue)
		title := writeID
		var rules []Rule

		if readID != "" || continueLast || m.Config.ShowLast {
			found, err := m.findReadID(readID)
			if err != nil {
				return modsError{
					Err:        err,
					ReasonText: "Could not find the conversation.",
				}
			}
			if found != nil {
				readID = found.ID
				if m.Config.Continue != "" && m.Config.Title == "" {
					title = found.Title
				}
				if !m.Config.NoCache {
					rules, err = m.db.ApprovalRules(readID)
					if err != nil {
						return modsError{
							Err:        err,
							ReasonText: "Could not load conversation approval rules.",
						}
					}
				}
			}
		}

		if continueLast {
			writeID = readID
		}

		if writeID == "" {
			writeID = NewID()
		}

		if !IDPattern.MatchString(writeID) {
			convo, err := m.db.Find(writeID)
			if err != nil {
				writeID = NewID()
			} else {
				writeID = convo.ID
			}
		}

		debug.Printf("Conversation: write_id=%s, read_id=%s, continue_last=%v, title=%s",
			writeID[:min(ShortIDLength, len(writeID))], readID[:min(ShortIDLength, len(readID))], continueLast, title)

		return cacheDetailsMsg{
			WriteID: writeID,
			Title:   title,
			ReadID:  readID,
			Rules:   rules,
		}
	}
}

func (m *Mods) findReadID(in string) (*Conversation, error) {
	if in != "" {
		return m.db.Find(in)
	}
	if (m.Config.ContinueLast || m.Config.ShowLast || m.Config.Continue != "") && m.Config.Show == "" {
		convo, err := m.db.FindHEAD()
		if err != nil {
			return nil, err
		}
		return convo, nil
	}
	return nil, ErrNoMatches
}

func (m *Mods) readStdinCmd() tea.Msg {
	if !IsInputTTY() {
		reader := bufio.NewReader(os.Stdin)
		stdinBytes, err := m.readLimitedStdin(reader)
		if err != nil {
			return modsError{Err: err, ReasonText: "Unable to read stdin."}
		}

		debug.Printf("Stdin: pipe mode, %d bytes read", len(stdinBytes))
		debug.Printf("Stdin image mode: %v", m.Config.StdinImage)

		if m.Config.StdinImage {
			return stdinImageInput{data: stdinBytes}
		}
		return completionInput{IncreaseIndent(string(stdinBytes))}
	}
	debug.Printf("Stdin: TTY mode, no piped input")
	return completionInput{""}
}

func (m *Mods) readLimitedStdin(reader io.Reader) ([]byte, error) {
	if m.Config.StdinImage || m.Config.NoLimit || m.Config.MaxInputChars <= 0 {
		return io.ReadAll(reader)
	}
	limit := m.Config.MaxInputChars
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) <= limit {
		return data, nil
	}
	end := int(limit)
	for end > 0 && (data[end]&0xc0) == 0x80 {
		end--
	}
	data = data[:end]
	data = append(data, []byte(fmt.Sprintf("\n\n[Input truncated at %d chars. Use --no-limit to disable truncation.]", limit))...)
	return data, nil
}

func (m *Mods) readFromCache() tea.Cmd {
	return func() tea.Msg {
		var messages []proto.Message
		if err := m.db.ReadMessages(m.Config.CacheReadFromID, &messages); err != nil {
			return modsError{Err: err, ReasonText: "There was an error loading the conversation."}
		}

		m.appendToOutput(proto.Conversation(messages).String())
		return streamEventMsg{kind: streamEventDone}
	}
}
