package main

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
		bts, err := io.ReadAll(resp.Body)
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
