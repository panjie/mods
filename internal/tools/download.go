package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/netutil"
	"github.com/panjie/mods/internal/pathutil"
	"github.com/panjie/mods/internal/proto"
)

const downloadFileLimit = 16 << 20

type downloadItem struct {
	URL  string `json:"url"`
	Path string `json:"path"`
}
type downloadArgs struct {
	Files     []downloadItem `json:"files"`
	Overwrite bool           `json:"overwrite"`
}
type downloadResult struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Bytes  int64  `json:"bytes,omitempty"`
	Error  string `json:"error,omitempty"`
}

func validateDownloadURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("download URL must be HTTP(S), with a host and without embedded credentials or fragments")
	}
	if strings.EqualFold(u.Hostname(), "raw.githubusercontent.com") && len(strings.Split(strings.Trim(u.Path, "/"), "/")) < 4 {
		return fmt.Errorf("GitHub raw URL requires owner, repository, revision and file path")
	}
	return nil
}

func parseDownloads(root string, data json.RawMessage) (downloadArgs, error) {
	var args downloadArgs
	if err := json.Unmarshal(data, &args); err != nil {
		return args, fmt.Errorf("invalid download arguments")
	}
	if len(args.Files) == 0 || len(args.Files) > 32 {
		return args, fmt.Errorf("files must contain 1 to 32 downloads")
	}
	seen := map[string]bool{}
	for i, item := range args.Files {
		if err := validateDownloadURL(item.URL); err != nil {
			return args, fmt.Errorf("files[%d]: %w", i, err)
		}
		if item.Path == "" || strings.ContainsAny(item.Path, "\x00\r\n") {
			return args, fmt.Errorf("files[%d]: a literal file path is required", i)
		}
		p := pathutil.NormalizePath(item.Path, pathutil.DefaultOptions(root, pathutil.FlavorPOSIX))
		key := strings.ToLower(p) // conservatively reject case aliases on every platform
		if seen[key] {
			return args, fmt.Errorf("duplicate download target at files[%d]", i)
		}
		seen[key] = true
		args.Files[i].Path = p
	}
	return args, nil
}

// RegisterDownload uses the same path authorization as filesystem tools.
func RegisterDownload(registry *Registry, cfg FilesystemConfig) error {
	return registry.Register(Tool{
		Kind:         ToolKindBuiltin,
		Capabilities: ToolCapabilities{Mutable: true},
		Validate:     func(data json.RawMessage) error { _, err := parseDownloads(cfg.Root, data); return err },
		IntentExtractor: func(data json.RawMessage) approval.AccessIntent {
			args, err := parseDownloads(cfg.Root, data)
			if err != nil {
				return approval.AccessIntent{Class: approval.AccessWrite, UncertainEffect: true}
			}
			intent := approval.AccessIntent{WriteDirs: []string{}, ReadOrigins: []string{}}
			for _, item := range args.Files {
				intent.WriteDirs = append(intent.WriteDirs, filepath.Dir(item.Path))
				u, _ := url.Parse(item.URL)
				intent.ReadOrigins = append(intent.ReadOrigins, u.Scheme+"://"+u.Host)
			}
			return intent
		},
		Spec: proto.ToolSpec{
			Name:        "http_download",
			Description: "Download 1-32 HTTP(S) files to literal paths without a shell. Prefer this for batch downloads. Create parent directories with fs_mkdir first. Each file is limited to 16 MiB; the batch stops on the first failure and reports completed/failed/skipped items. Existing files are preserved unless overwrite=true. Downloads never execute content. Private/loopback destinations are refused unless the user configured MODS_WEB_SEARCH_ALLOW_PRIVATE=1.",
			InputSchema: objectSchema(map[string]any{
				"files":     map[string]any{"type": "array", "minItems": 1, "maxItems": 32, "items": objectSchema(map[string]any{"url": stringProp("Complete HTTP(S) source URL."), "path": stringProp("Literal destination file path.")}, "url", "path")},
				"overwrite": booleanProp("Explicitly replace existing regular files; defaults to false."),
			}, "files"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) { return runDownloads(ctx, cfg, data) },
	})
}

func runDownloads(ctx context.Context, cfg FilesystemConfig, data json.RawMessage) (string, error) {
	args, err := parseDownloads(cfg.Root, data)
	if err != nil {
		return "", err
	}
	// Validate every local target before the first network request or write.
	for i := range args.Files {
		p, err := resolveWorkspacePathNoFollowLeaf(ctx, cfg.Root, args.Files[i].Path, cfg.SafeDirs)
		if err != nil {
			return "", err
		}
		info, err := os.Lstat(p)
		if err == nil && (!args.Overwrite || !info.Mode().IsRegular()) {
			return "", fmt.Errorf("download target exists or is not a regular file: %s", p)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent, err := os.Stat(filepath.Dir(p))
		if err != nil {
			return "", err
		}
		if !parent.IsDir() {
			return "", fmt.Errorf("download parent is not a directory")
		}
		args.Files[i].Path = p
	}
	transport := netutil.SafeTransport(netutil.SafeTransportOptions{AllowPrivate: func() bool { return os.Getenv("MODS_WEB_SEARCH_ALLOW_PRIVATE") == "1" }, ErrorPrefix: "download"})
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 60 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		if len(via) > 0 && via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return fmt.Errorf("refusing HTTPS downgrade")
		}
		return validateDownloadURL(req.URL.String())
	}}
	results := make([]downloadResult, 0, len(args.Files))
	var failed error
	for _, item := range args.Files {
		r := downloadResult{Path: item.Path, Status: "skipped"}
		if failed == nil {
			r.Bytes, failed = downloadOne(ctx, client, item, args.Overwrite)
			r.Status = "completed"
			if failed != nil {
				r.Status = "failed"
				r.Error = failed.Error()
			}
		}
		results = append(results, r)
	}
	output, _ := json.Marshal(results)
	return string(output), failed
}

func downloadOne(ctx context.Context, client *http.Client, item downloadItem, overwrite bool) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, item.URL, nil)
	if err != nil {
		return 0, fmt.Errorf("invalid download request")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("download request failed (network policy, redirect, cancellation or connection error)")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("download HTTP status %d", resp.StatusCode)
	}
	if resp.ContentLength > downloadFileLimit {
		return 0, fmt.Errorf("download exceeds 16 MiB")
	}
	f, err := os.CreateTemp(filepath.Dir(item.Path), ".mods-download-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(f.Name())
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, downloadFileLimit+1))
	closeErr := f.Close()
	if copyErr != nil {
		return n, fmt.Errorf("download body could not be read")
	}
	if closeErr != nil {
		return n, closeErr
	}
	if n > downloadFileLimit {
		return n, fmt.Errorf("download exceeds 16 MiB")
	}
	if err := ctx.Err(); err != nil {
		return n, err
	}
	if overwrite {
		// Rename replaces the directory entry, never follows a leaf symlink.
		err = os.Rename(f.Name(), item.Path)
	} else {
		// Link publishes the completed file without an overwrite race.
		err = os.Link(f.Name(), item.Path)
	}
	return n, err
}
