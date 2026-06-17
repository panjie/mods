package main

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/mods/internal/proto"
	"github.com/charmbracelet/mods/internal/stream"
)

func (m *Mods) classifyShellCommand(command string) bool {
	if isObviouslyReadOnly(command) {
		debugPrintf("classifyShellCommand: cmd=%q classified as read-only by heuristic, auto-approving", truncateStr(command, 80))
		return false
	}

	if cached, ok := shellClassifyCache.Load(command); ok {
		needsReview := cached.(bool)
		debugPrintf("classifyShellCommand: cmd=%q cached -> needsReview=%v", truncateStr(command, 80), needsReview)
		return needsReview
	}

	cfg := m.Config
	api, mod, err := m.resolveModel(cfg)
	if err != nil {
		return true
	}

	cfgs, err := m.buildProviderConfigs(mod, api)
	if err != nil {
		return true
	}
	accfg := cfgs.Anthropic
	gccfg := cfgs.Google
	cccfg := cfgs.Cohere
	occfg := cfgs.Ollama
	ccfg := cfgs.OpenAI

	classifyCtx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()

	system := m.Config.ShellClassifyPrompt
	if system == "" {
		system = "Classify this shell command. Does it create, delete, or modify any files, directories, system settings, or persistent state? Answer only YES or NO. If unsure, answer YES."
	}
	debugPrintf("classifyShellCommand: using model=%s api=%s, system=%q", mod.Name, mod.API, system)
	request := proto.Request{
		Messages: []proto.Message{
			{Role: proto.RoleSystem, Content: system},
			{Role: proto.RoleUser, Content: command},
		},
		Model:       mod.Name,
		Temperature: ptrOrNil(cfg.Temperature),
	}

	client, err := newStreamClient(mod.API, accfg, gccfg, cccfg, occfg, ccfg)
	if err != nil {
		return true
	}

	st := client.Request(classifyCtx, request)
	defer st.Close()

	var sb strings.Builder
	for st.Next() {
		chunk, err := st.Current()
		if err != nil && !errors.Is(err, stream.ErrNoContent) {
			return true
		}
		sb.WriteString(chunk.Content)
	}
	if st.Err() != nil {
		return true
	}
	rawResponse := strings.TrimSpace(sb.String())
	upper := strings.ToUpper(rawResponse)
	hasYes := reYes.MatchString(upper)
	hasNo := reNo.MatchString(upper)
	needsReview := !hasNo || hasYes
	debugPrintf("classifyShellCommand: cmd=%q resp=%s hasYes=%v hasNo=%v -> needsReview=%v",
		command, truncateStr(rawResponse, 80), hasYes, hasNo, needsReview)

	shellClassifyCache.Store(command, needsReview)
	return needsReview
}

var reYes = regexp.MustCompile(`\bYES\b`)
var reNo = regexp.MustCompile(`\bNO\b`)

var shellClassifyCache sync.Map

var reReadOnlyCmd = regexp.MustCompile(`^(echo|dir|type|whoami|hostname|ver|date|time|path|cd|chdir|where|pwd)\b|^set(\s|$)`)

var rePwshMutOp = regexp.MustCompile(`(?i)\b(Remove-Item|Delete|Set-Content|Add-Content|Out-File|New-Item|Copy-Item|Move-Item|Rename-Item|Clear-Content|Stop-Process|Start-Process|Invoke-Item|Set-|New-|Remove-|Clear-|mkdir|rmdir|del\s|rd\s|ren\s|rm\b|ri\b|erase\b|kill\b|sc\b|cp\b|copy\b|mv\b|move\b|ni\b|sp\b)\b`)

var rePwshReadCmd = regexp.MustCompile(`(?i)\bpowershell(?:\.exe)?\s+-`)

var reDirectPwshReadCmd = regexp.MustCompile(`(?i)^(\$PSVersionTable\b|Get-|Measure-Object\b|Select-Object\b|Sort-Object\b|Format-|ConvertTo-|Write-Output\b)`)

var rePwshComplexSyntax = regexp.MustCompile(`[|;{}&]`)

func hasShellRedirect(cmd string) bool {
	for i := 0; i < len(cmd); i++ {
		if cmd[i] == '>' {
			rest := strings.TrimSpace(cmd[i+1:])
			lower := strings.ToLower(rest)
			if strings.HasPrefix(lower, "nul") && (len(rest) == 3 || rest[3] == ' ' || rest[3] == '&' || rest[3] == '|') {
				continue
			}
			return true
		}
	}
	return false
}

func hasPowerShellComplexSyntax(cmd string) bool {
	return rePwshComplexSyntax.MatchString(cmd)
}

func isObviouslyReadOnly(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	lower := strings.ToLower(trimmed)

	if reReadOnlyCmd.MatchString(lower) {
		if hasShellRedirect(trimmed) {
			return false
		}
		return true
	}

	if rePwshReadCmd.MatchString(lower) {
		if rePwshMutOp.MatchString(trimmed) || hasShellRedirect(trimmed) || hasPowerShellComplexSyntax(trimmed) {
			return false
		}
		return true
	}

	if reDirectPwshReadCmd.MatchString(trimmed) {
		if rePwshMutOp.MatchString(trimmed) || hasShellRedirect(trimmed) || hasPowerShellComplexSyntax(trimmed) {
			return false
		}
		return true
	}

	return false
}
