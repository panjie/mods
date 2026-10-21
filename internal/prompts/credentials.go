package prompts

// InteractiveCredentialsForOS keeps Unix askpass promises off Windows.
func InteractiveCredentialsForOS(goos string) string {
	if goos == "windows" {
		return WindowsInteractiveCredentials
	}
	return InteractiveCredentials
}
