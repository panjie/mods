package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const loadMsgTimeout = 10 * time.Second
const maxLoadMsgBytes = 1 << 20

func loadMsg(ctx context.Context, msg string) (string, error) {
	if strings.HasPrefix(msg, "https://") || strings.HasPrefix(msg, "http://") {
		reqCtx, cancel := context.WithTimeout(ctx, loadMsgTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, msg, nil)
		if err != nil {
			return "", fmt.Errorf("load msg: %w", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("load msg: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return "", fmt.Errorf("load msg: %s returned HTTP %d", msg, resp.StatusCode)
		}
		bts, err := readLimited(resp.Body, maxLoadMsgBytes)
		if err != nil {
			return "", fmt.Errorf("load msg: %w", err)
		}
		return string(bts), nil
	}

	if strings.HasPrefix(msg, "file://") {
		bts, err := os.ReadFile(strings.TrimPrefix(msg, "file://"))
		if err != nil {
			return "", fmt.Errorf("load msg: %w", err)
		}
		return string(bts), nil
	}

	return msg, nil
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	bts, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(bts)) > limit {
		return nil, fmt.Errorf("message exceeds %d bytes", limit)
	}
	return bts, nil
}
