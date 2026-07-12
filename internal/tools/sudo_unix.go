//go:build !windows

package tools

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"mvdan.cc/sh/v3/syntax"
)

const SudoAskpassHelperArg = "__mods_sudo_askpass"

var sudoWordPattern = regexp.MustCompile(`(^|[^A-Za-z0-9_])sudo([^A-Za-z0-9_]|$)`)

type preparedSudoCommand struct {
	Command string
	Env     map[string]string
}

type askpassRequest struct {
	Token  string `json:"token"`
	Prompt string `json:"prompt"`
}

type askpassResponse struct {
	Password string `json:"password,omitempty"`
	Error    string `json:"error,omitempty"`
}

// prepareSudoCommand rewrites statically identifiable sudo calls. Interactive
// runs use -A and a private askpass broker; non-interactive runs use -n so a
// missing credential fails immediately instead of hanging on an unavailable TTY.
func prepareSudoCommand(ctx context.Context, command string, prompt SecretPromptHandler) (preparedSudoCommand, func(), error) {
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		if sudoWordPattern.MatchString(command) {
			return preparedSudoCommand{}, func() {}, fmt.Errorf("sudo command could not be parsed safely; use a direct sudo invocation")
		}
		return preparedSudoCommand{Command: command}, func() {}, nil
	}
	found := false
	needsAskpass := false
	var rewriteErr error
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 || rewriteErr != nil {
			return rewriteErr == nil
		}
		name, ok := staticWord(call.Args[0])
		if !ok || name != "sudo" {
			return true
		}
		found = true
		hasMode := false
		for _, word := range call.Args[1:] {
			arg, ok := staticWord(word)
			if !ok {
				continue
			}
			switch arg {
			case "-S", "--stdin":
				rewriteErr = fmt.Errorf("sudo -S is not supported; remove -S so mods can request the password securely")
				return false
			case "-n", "--non-interactive":
				hasMode = true
			case "-A", "--askpass":
				hasMode = true
				needsAskpass = true
			}
			if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") {
				flags := strings.TrimPrefix(arg, "-")
				if strings.Contains(flags, "S") {
					rewriteErr = fmt.Errorf("sudo -S is not supported; remove -S so mods can request the password securely")
					return false
				}
				if strings.Contains(flags, "n") {
					hasMode = true
				}
				if strings.Contains(flags, "A") {
					hasMode = true
					needsAskpass = true
				}
			}
		}
		if hasMode {
			return true
		}
		flag := "-n"
		if prompt != nil {
			flag = "-A"
			needsAskpass = true
		}
		call.Args = append(call.Args[:1], append([]*syntax.Word{literalWord(flag)}, call.Args[1:]...)...)
		return true
	})
	if rewriteErr != nil {
		return preparedSudoCommand{}, func() {}, rewriteErr
	}
	if !found {
		if sudoWordPattern.MatchString(command) {
			return preparedSudoCommand{}, func() {}, fmt.Errorf("nested or dynamically constructed sudo is not supported; use a direct sudo invocation")
		}
		return preparedSudoCommand{Command: command}, func() {}, nil
	}
	var rendered bytes.Buffer
	if err := syntax.NewPrinter(syntax.SingleLine(true)).Print(&rendered, file); err != nil {
		return preparedSudoCommand{}, func() {}, fmt.Errorf("prepare sudo command: %w", err)
	}
	prepared := preparedSudoCommand{Command: strings.TrimSpace(rendered.String())}
	if !needsAskpass {
		return prepared, func() {}, nil
	}
	if prompt == nil {
		return preparedSudoCommand{}, func() {}, fmt.Errorf("sudo password input requires an interactive terminal")
	}
	helper, cleanup, err := startAskpassBroker(ctx, command, prompt)
	if err != nil {
		return preparedSudoCommand{}, func() {}, err
	}
	prepared.Env = map[string]string{"SUDO_ASKPASS": helper}
	return prepared, cleanup, nil
}

func staticWord(word *syntax.Word) (string, bool) {
	if word == nil || len(word.Parts) != 1 {
		return "", false
	}
	lit, ok := word.Parts[0].(*syntax.Lit)
	if !ok {
		return "", false
	}
	return lit.Value, true
}

func literalWord(value string) *syntax.Word {
	return &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: value}}}
}

func startAskpassBroker(ctx context.Context, command string, prompt SecretPromptHandler) (string, func(), error) {
	dir, err := os.MkdirTemp("", "mods-sudo-askpass-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("create sudo askpass directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", func() {}, fmt.Errorf("secure sudo askpass directory: %w", err)
	}
	socketPath := filepath.Join(dir, "broker.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", func() {}, fmt.Errorf("start sudo askpass broker: %w", err)
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		_ = listener.Close()
		_ = os.RemoveAll(dir)
		return "", func() {}, fmt.Errorf("create sudo askpass token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)
	exe, err := os.Executable()
	if err != nil {
		_ = listener.Close()
		_ = os.RemoveAll(dir)
		return "", func() {}, fmt.Errorf("locate mods executable: %w", err)
	}
	helper := filepath.Join(dir, "askpass.sh")
	script := fmt.Sprintf("#!/bin/sh\nexec %s %s %s %s \"$@\"\n", shellQuote(exe), SudoAskpassHelperArg, shellQuote(socketPath), shellQuote(token))
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		_ = listener.Close()
		_ = os.RemoveAll(dir)
		return "", func() {}, fmt.Errorf("create sudo askpass helper: %w", err)
	}
	var once sync.Once
	done := make(chan struct{})
	cleanup := func() {
		once.Do(func() {
			close(done)
			_ = listener.Close()
			_ = os.RemoveAll(dir)
		})
	}
	go serveAskpass(ctx, done, listener, token, command, prompt)
	return helper, cleanup, nil
}

func serveAskpass(ctx context.Context, done <-chan struct{}, listener net.Listener, token, command string, prompt SecretPromptHandler) {
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
		case <-done:
		}
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		func() {
			defer conn.Close()
			var req askpassRequest
			if err := json.NewDecoder(conn).Decode(&req); err != nil || req.Token != token {
				_ = json.NewEncoder(conn).Encode(askpassResponse{Error: "unauthorized askpass request"})
				return
			}
			password, err := prompt(ctx, req.Prompt, command)
			if err != nil {
				_ = json.NewEncoder(conn).Encode(askpassResponse{Error: err.Error()})
				return
			}
			_ = json.NewEncoder(conn).Encode(askpassResponse{Password: password})
		}()
	}
}

// RunSudoAskpassHelper handles the private helper mode invoked by sudo.
func RunSudoAskpassHelper(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("invalid sudo askpass helper arguments")
	}
	prompt := "sudo password"
	if len(args) > 2 && strings.TrimSpace(args[2]) != "" {
		prompt = args[2]
	}
	conn, err := net.Dial("unix", args[0])
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(askpassRequest{Token: args[1], Prompt: prompt}); err != nil {
		return err
	}
	var resp askpassResponse
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	_, err = fmt.Fprintln(os.Stdout, resp.Password)
	return err
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
