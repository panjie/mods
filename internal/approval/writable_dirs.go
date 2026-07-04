package approval

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Writable-directory extraction. For a given shell command, determine
// which filesystem paths the command could create, modify, or delete,
// so the reviewer can decide whether the operation falls inside an
// approved directory. POSIX commands go through the mvdan parser;
// PowerShell / Windows commands use the simple tokenizer.

// ExtractWritableDirs returns filesystem directories that a shell command can
// create, modify, or delete. The result is best-effort and may be empty when
// the command is not statically understood.
func ExtractWritableDirs(command string, posix bool) []string {
	return extractWritableDirs(command, posix)
}

func extractWritableDirs(command string, posix bool) []string {
	normalized := normalizeShellCommandWithMode(command, posix)
	if normalized == "" {
		return nil
	}
	if !posix {
		return extractWritableDirsSimple(normalized)
	}
	return extractWritableDirsPOSIX(command)
}

func extractWritableDirsPOSIX(command string) []string {
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return nil
	}
	var dirs []string
	for _, stmt := range file.Stmts {
		collectWritableDirsFromStmt(stmt, &dirs)
	}
	if len(dirs) == 0 {
		return nil
	}
	return dedupeSorted(dirs)
}

func collectWritableDirsFromStmt(stmt *syntax.Stmt, dirs *[]string) {
	if stmt == nil || stmt.Cmd == nil {
		return
	}
	for _, redir := range stmt.Redirs {
		if redir == nil || redir.Word == nil || !redirectionWrites(redir.Op) {
			continue
		}
		target, ok := staticShellWord(redir.Word)
		if !ok || target == "" {
			continue
		}
		*dirs = append(*dirs, parentDir(target))
	}
	if binary, ok := stmt.Cmd.(*syntax.BinaryCmd); ok {
		collectWritableDirsFromStmt(binary.X, dirs)
		collectWritableDirsFromStmt(binary.Y, dirs)
		return
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 {
		return
	}
	args := shellWords(call.Args)
	if len(args) == 0 {
		return
	}
	*dirs = append(*dirs, writableDirsFromTokens(args, true)...)
}

func extractWritableDirsSimple(command string) []string {
	parts := splitSimpleCompound(normalizeSimpleCommand(command))
	if len(parts) == 0 {
		return nil
	}
	var dirs []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		dirs = append(dirs, writableDirsFromRedirection(part)...)
		tokens := tokenizeSimple(part)
		if len(tokens) == 0 {
			continue
		}
		dirs = append(dirs, writableDirsFromTokens(tokens, false)...)
	}
	if len(dirs) == 0 {
		return nil
	}
	return dedupeSorted(dirs)
}

// writableDirsFromTokens maps a tokenized command (args[0] = program
// name) to the directories it could write. The dispatch is a hardcoded
// table of common POSIX utilities and PowerShell cmdlets; anything not
// recognized returns nil (fail-closed at the caller level).
func writableDirsFromTokens(args []string, posix bool) []string {
	if len(args) == 0 {
		return nil
	}
	command := args[0]
	if !posix {
		command = strings.ToLower(command)
	}
	switch command {
	case "rm", "rmdir", "unlink", "touch", "chmod", "chown":
		return parentDirs(commandOperands(args[1:]))
	case "mkdir":
		return parentDirs(commandOperands(args[1:]))
	case "cp", "mv":
		operands := commandOperands(args[1:])
		if len(operands) == 0 {
			return nil
		}
		return []string{destinationDir(operands[len(operands)-1])}
	case "tee":
		return parentDirs(commandOperands(args[1:]))
	case "remove-item", "del", "erase", "rd":
		if paths := powerShellParamValues(args, "path", "literalpath"); len(paths) > 0 {
			return parentDirs(paths)
		}
		return parentDirs(commandOperands(args[1:]))
	case "copy-item", "move-item":
		if destinations := powerShellParamValues(args, "destination"); len(destinations) > 0 {
			return destinationDirs(destinations)
		}
		operands := commandOperands(args[1:])
		if len(operands) == 0 {
			return nil
		}
		return []string{destinationDir(operands[len(operands)-1])}
	case "copy", "move":
		operands := commandOperands(args[1:])
		if len(operands) == 0 {
			return nil
		}
		return []string{destinationDir(operands[len(operands)-1])}
	case "new-item", "set-content", "add-content":
		if paths := powerShellParamValues(args, "path", "literalpath"); len(paths) > 0 {
			return parentDirs(paths)
		}
		return parentDirs(commandOperands(args[1:]))
	case "out-file":
		if paths := powerShellParamValues(args, "filepath", "literalpath", "path"); len(paths) > 0 {
			return parentDirs(paths)
		}
		return parentDirs(commandOperands(args[1:]))
	default:
		return nil
	}
}

func powerShellParamValues(args []string, names ...string) []string {
	nameSet := make(map[string]struct{}, len(names))
	for _, name := range names {
		nameSet[strings.ToLower(name)] = struct{}{}
	}
	var values []string
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			continue
		}
		key := strings.TrimLeft(arg, "-")
		var inlineValue string
		if before, after, ok := strings.Cut(key, ":"); ok {
			key = before
			inlineValue = after
		}
		if _, ok := nameSet[strings.ToLower(key)]; !ok {
			continue
		}
		if inlineValue != "" {
			values = append(values, inlineValue)
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			values = append(values, args[i+1])
			i++
		}
	}
	return values
}

func commandOperands(args []string) []string {
	operands := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "" {
			continue
		}
		if arg == "--" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		operands = append(operands, arg)
	}
	return operands
}

func parentDirs(paths []string) []string {
	dirs := make([]string, 0, len(paths))
	for _, path := range paths {
		dirs = append(dirs, parentDir(path))
	}
	return dirs
}

func destinationDirs(paths []string) []string {
	dirs := make([]string, 0, len(paths))
	for _, path := range paths {
		dirs = append(dirs, destinationDir(path))
	}
	return dirs
}

func writableDirsFromRedirection(command string) []string {
	tokens := tokenizeSimple(command)
	dirs := make([]string, 0)
	for i, token := range tokens {
		if !isRedirectionToken(token) || i+1 >= len(tokens) {
			continue
		}
		dirs = append(dirs, parentDir(tokens[i+1]))
	}
	return dirs
}
