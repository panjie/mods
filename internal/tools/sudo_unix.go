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

type preparedSudoProcess struct {
	Args []string
	Env  map[string]string
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
		args := make([]string, 0, len(call.Args)-1)
		for _, word := range call.Args[1:] {
			arg, ok := staticWord(word)
			if !ok {
				// Preserve the argument position so an option such as -u still
				// consumes its dynamic value before authentication flags are read.
				arg = ""
			}
			args = append(args, arg)
		}
		hasMode, callNeedsAskpass, err := sudoAuthMode(args)
		if err != nil {
			rewriteErr = err
			return false
		}
		if callNeedsAskpass {
			needsAskpass = true
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

// prepareSudoProcess applies the same non-blocking sudo policy as
// prepareSudoCommand to a literal process invocation. Without this, a direct
// process_run of sudo can wait on the terminal behind the TUI and never reach
// mods' secret-input flow.
func prepareSudoProcess(ctx context.Context, program string, args []string, prompt SecretPromptHandler, display string) (preparedSudoProcess, func(), error) {
	prepared := preparedSudoProcess{Args: args}
	if filepath.Base(program) != "sudo" {
		return prepared, func() {}, nil
	}

	hasMode, needsAskpass, err := sudoAuthMode(args)
	if err != nil {
		return preparedSudoProcess{}, func() {}, err
	}
	if !hasMode {
		flag := "-n"
		if prompt != nil {
			flag = "-A"
			needsAskpass = true
		}
		prepared.Args = append([]string{flag}, args...)
	}
	if !needsAskpass {
		return prepared, func() {}, nil
	}
	if prompt == nil {
		return preparedSudoProcess{}, func() {}, fmt.Errorf("sudo password input requires an interactive terminal")
	}
	helper, cleanup, err := startAskpassBroker(ctx, display, prompt)
	if err != nil {
		return preparedSudoProcess{}, func() {}, err
	}
	prepared.Env = map[string]string{"SUDO_ASKPASS": helper}
	return prepared, cleanup, nil
}

func sudoAuthMode(args []string) (hasMode, needsAskpass bool, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || arg == "-" || !strings.HasPrefix(arg, "-") {
			break
		}
		if strings.HasPrefix(arg, "--") {
			name := arg
			if before, _, ok := strings.Cut(arg, "="); ok {
				name = before
			}
			switch name {
			case "--stdin":
				return false, false, fmt.Errorf("sudo -S is not supported; remove -S so mods can request the password securely")
			case "--non-interactive":
				hasMode = true
			case "--askpass":
				hasMode = true
				needsAskpass = true
			}
			if !strings.Contains(arg, "=") && sudoLongOptionTakesValue(name) {
				i++
			}
			continue
		}

		flags := strings.TrimPrefix(arg, "-")
		for j := 0; j < len(flags); j++ {
			switch flags[j] {
			case 'S':
				return false, false, fmt.Errorf("sudo -S is not supported; remove -S so mods can request the password securely")
			case 'n':
				hasMode = true
			case 'A':
				hasMode = true
				needsAskpass = true
			}
			if sudoShortOptionTakesValue(flags[j]) {
				if j == len(flags)-1 {
					i++
				}
				break
			}
		}
	}
	return hasMode, needsAskpass, nil
}

func sudoShortOptionTakesValue(flag byte) bool {
	return strings.ContainsRune("CDghpRTUu", rune(flag))
}

func sudoLongOptionTakesValue(option string) bool {
	switch option {
	case "--chdir", "--chroot", "--close-from", "--command-timeout", "--group", "--host", "--other-user", "--prompt", "--user":
		return true
	default:
		return false
	}
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
