package openai

import (
	"bufio"
	"bytes"
	"io"

	"github.com/openai/openai-go/packages/ssestream"
)

// The stock openai-go eventStreamDecoder dispatches an event on every blank
// line, so a comment-only keep-alive (": PROCESSING\n\n") from gateways such
// as MiMo's becomes an event with empty data that fails JSON decoding with
// "unexpected end of JSON input". Per the SSE spec comments must be ignored,
// so replace the decoder for the exact text/event-stream content type.
func init() {
	ssestream.RegisterDecoder("text/event-stream", newKeepAliveSafeDecoder)
}

// keepAliveSafeDecoder mirrors openai-go's eventStreamDecoder except that
// blank lines after comment-only (or stray) lines do not dispatch an event.
type keepAliveSafeDecoder struct {
	evt ssestream.Event
	rc  io.ReadCloser
	scn *bufio.Scanner
}

func newKeepAliveSafeDecoder(rc io.ReadCloser) ssestream.Decoder {
	scn := bufio.NewScanner(rc)
	scn.Buffer(nil, bufio.MaxScanTokenSize<<9)
	return &keepAliveSafeDecoder{rc: rc, scn: scn}
}

func (s *keepAliveSafeDecoder) Next() bool {
	event := ""
	data := bytes.NewBuffer(nil)

	for s.scn.Scan() {
		txt := s.scn.Bytes()

		if len(txt) == 0 {
			if event == "" && data.Len() == 0 {
				continue
			}
			s.evt = ssestream.Event{Type: event, Data: data.Bytes()}
			return true
		}

		name, value, _ := bytes.Cut(txt, []byte(":"))
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}

		switch string(name) {
		case "":
			continue
		case "event":
			event = string(value)
		case "data":
			_, _ = data.Write(value)
			_, _ = data.WriteRune('\n')
		}
	}

	return false
}

func (s *keepAliveSafeDecoder) Event() ssestream.Event {
	return s.evt
}

func (s *keepAliveSafeDecoder) Close() error {
	return s.rc.Close()
}

func (s *keepAliveSafeDecoder) Err() error {
	return s.scn.Err()
}
