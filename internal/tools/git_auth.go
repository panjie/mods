package tools

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const GitAskpassHelperArg = "__mods_git_askpass"

const (
	gitAuthAddressEnv = "MODS_GIT_AUTH_ADDRESS"
	gitAuthTokenEnv   = "MODS_GIT_AUTH_TOKEN"
	gitAuthMaxMessage = 16 << 10
)

// GitAuthRequest is an input request from a running command, not a model tool call.
// Prompt is untrusted text from the child process and must be sanitized for display.
type GitAuthRequest struct {
	Prompt   string
	Command  string
	Username bool
}

type GitAuthHandler func(context.Context, GitAuthRequest) (string, error)

type nonInteractiveGitAuthKey struct{}

// WithoutGitAuthInput suppresses runtime credential prompts during discovery.
func WithoutGitAuthInput(ctx context.Context) context.Context {
	return context.WithValue(ctx, nonInteractiveGitAuthKey{}, true)
}

type gitAskpassRequest struct {
	Token  string `json:"token"`
	Prompt string `json:"prompt"`
}

type gitAskpassResponse struct {
	Value string `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

// commandEnv merges overrides without duplicate keys, including on Windows.
func commandEnv(base []string, overrides map[string]string) []string {
	out := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		replaced := false
		for name := range overrides {
			if key == name || (runtime.GOOS == "windows" && strings.EqualFold(key, name)) {
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, entry)
		}
	}
	for key, value := range overrides {
		out = append(out, key+"="+value)
	}
	return out
}

func envValue(env []string, name string) string {
	value, _ := lookupCommandEnv(env, name)
	return value
}

func lookupCommandEnv(env []string, name string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		key, value, _ := strings.Cut(env[i], "=")
		if key == name || (runtime.GOOS == "windows" && strings.EqualFold(key, name)) {
			return value, true
		}
	}
	return "", false
}

// prepareGitAuth installs only Git's last askpass fallback. Git itself retains
// precedence for credential helpers, GIT_ASKPASS and core.askPass. A user's
// existing SSH_ASKPASS is left intact too. No Git config or command is rewritten.
func prepareGitAuth(ctx context.Context, cmd *exec.Cmd, display string, prompt GitAuthHandler) (func(), error) {
	if disabled, _ := ctx.Value(nonInteractiveGitAuthKey{}).(bool); disabled {
		prompt = nil
	}
	env := cmd.Environ()
	cmd.Env = commandEnv(env, map[string]string{"GIT_TERMINAL_PROMPT": "0"})
	_, existingAskpass := lookupCommandEnv(env, "SSH_ASKPASS")
	if prompt == nil || existingAskpass {
		return func() {}, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate Git authentication helper: %w", err)
	}
	helper, address, token, cleanup, err := startGitAuthBroker(ctx, exe, display, prompt)
	if err != nil {
		return nil, err
	}
	cmd.Env = commandEnv(cmd.Env, map[string]string{
		"SSH_ASKPASS": helper, gitAuthAddressEnv: address, gitAuthTokenEnv: token,
	})
	return cleanup, nil
}

func startGitAuthBroker(parent context.Context, exe, display string, prompt GitAuthHandler) (helper, address, token string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "mods-git-auth-*")
	if err != nil {
		return "", "", "", nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(dir)
		}
	}()
	if err = os.Chmod(dir, 0o700); err != nil {
		return "", "", "", nil, err
	}
	var bytes [32]byte
	if _, err = rand.Read(bytes[:]); err != nil {
		return "", "", "", nil, err
	}
	token = hex.EncodeToString(bytes[:])
	listener, address, err := listenGitAuth(dir, token)
	if err != nil {
		return "", "", "", nil, err
	}
	defer func() {
		if !ok {
			_ = listener.Close()
		}
	}()
	helper = filepath.Join(dir, "askpass.sh")
	// Git for Windows recognizes #!/bin/sh using its bundled shell. Git passes
	// the helper path literally (not as shell source), so do not quote SSH_ASKPASS.
	quotedExe := "'" + strings.ReplaceAll(filepath.ToSlash(exe), "'", "'\\''") + "'"
	script := "#!/bin/sh\nexec " + quotedExe + " " + GitAskpassHelperArg + " \"$@\"\n"
	if err = os.WriteFile(helper, []byte(script), 0o700); err != nil {
		return "", "", "", nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	go func() { defer close(done); serveGitAuth(ctx, listener, token, display, prompt) }()
	var once sync.Once
	cleanup = func() { once.Do(func() { cancel(); _ = listener.Close(); <-done; stop(); _ = os.RemoveAll(dir) }) }
	ok = true
	return helper, address, token, cleanup, nil
}

func serveGitAuth(ctx context.Context, listener net.Listener, token, display string, prompt GitAuthHandler) {
	failed := false
	requests := 0
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		func() {
			defer conn.Close()
			stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
			defer stop()
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			var req gitAskpassRequest
			if err := json.NewDecoder(io.LimitReader(conn, gitAuthMaxMessage)).Decode(&req); err != nil || subtle.ConstantTimeCompare([]byte(req.Token), []byte(token)) != 1 {
				return
			}
			if deadline, ok := ctx.Deadline(); ok {
				_ = conn.SetDeadline(deadline)
			} else {
				_ = conn.SetDeadline(time.Time{})
			}
			response := gitAskpassResponse{}
			requests++
			switch {
			case ctx.Err() != nil || failed:
				response.Error = "Git authentication canceled"
			case requests > 32:
				response.Error = "too many Git authentication requests"
			case !strings.Contains(req.Prompt, "https://") && !strings.Contains(req.Prompt, "http://"):
				// SSH also reads SSH_ASKPASS; do not turn private-key or host-key
				// prompts into HTTP credential requests.
				response.Error = "mods askpass supports Git HTTP(S) authentication only"
			case prompt == nil:
				response.Error = "Git authentication requires interactive user input"
			default:
				value, err := prompt(ctx, GitAuthRequest{Prompt: req.Prompt, Command: display, Username: strings.HasPrefix(req.Prompt, "Username for ")})
				if err != nil {
					response.Error = "Git authentication canceled or unavailable"
				} else if len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") {
					response.Error = "Git authentication requires a single-line value"
				} else {
					response.Value = value
				}
			}
			if response.Error != "" {
				failed = true
			}
			_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
			_ = json.NewEncoder(conn).Encode(response)
		}()
	}
}

// RunGitAskpassHelper runs before normal CLI initialization. Its stdout belongs
// exclusively to Git, never to the model-facing command output stream.
func RunGitAskpassHelper(args []string) error {
	if len(args) != 1 || len(args[0]) > 8192 {
		return errors.New("invalid Git authentication request")
	}
	address, token := os.Getenv(gitAuthAddressEnv), os.Getenv(gitAuthTokenEnv)
	if address == "" || len(token) != 64 {
		return errors.New("Git authentication session unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := dialGitAuth(ctx, address)
	if err != nil {
		return errors.New("Git authentication session unavailable")
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err = json.NewEncoder(conn).Encode(gitAskpassRequest{Token: token, Prompt: args[0]}); err != nil {
		return errors.New("Git authentication session closed")
	}
	// The broker's command deadline bounds the wait and closes the connection.
	var response gitAskpassResponse
	if err = json.NewDecoder(io.LimitReader(conn, gitAuthMaxMessage)).Decode(&response); err != nil {
		return errors.New("Git authentication session closed")
	}
	if response.Error != "" {
		return errors.New(response.Error)
	}
	_, err = fmt.Fprintln(os.Stdout, response.Value)
	return err
}
