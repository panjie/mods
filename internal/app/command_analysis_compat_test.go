package app

// Focused discovery seams for existing integration tests. Production dispatch
// uses only Analyze; these wrappers contain no command or target semantics.
func (m *Mods) resolveGitPushOrigins(tool, command, cwd string) (origins, unresolved, local []string) {
	if tool == "process_run" {
		return nil, nil, nil
	}
	engine, ctx := m.commandAnalyzer()
	return engine.ResolveGitPushOrigins(ctx, !shellToolUsesPowerShell(tool), command, cwd)
}
func (m *Mods) resolveGitPushArgvOrigins(args []string, cwd string) (origins, unresolved, local []string) {
	engine, ctx := m.commandAnalyzer()
	return engine.ResolveGitPushArgvOrigins(ctx, args, cwd)
}
