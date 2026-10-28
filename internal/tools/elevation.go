package tools

import "runtime"

// ElevationHelperArg is a private, one-command helper entry point. It never
// opens configuration or the conversation database.
const ElevationHelperArg = "__mods_uac_exec"

const windowsElevationGuidance = " On Windows, set elevated=true only when administrator privileges are required. mods requests UAC for this command after ordinary approval and returns its output and exit status. Do not wrap commands in sudo, Start-Process -Verb RunAs, or scheduled tasks to elevate. UAC consent time counts toward timeout_ms; cancellation is not permission to retry."

func elevationDescription(description string) string {
	if runtime.GOOS == "windows" {
		return description + windowsElevationGuidance
	}
	return description
}

func commandSchema(properties map[string]any, required ...string) map[string]any {
	properties["effect"] = map[string]any{
		"type":        "string",
		"enum":        []string{string(CommandReadOnly), string(CommandMayWrite)},
		"description": "Declare the effect of the entire call, including subprocesses and scripts: read_only for reads/computation only; may_write for persistent changes or uncertainty. This declaration controls approval.",
	}
	required = append(required, "effect")
	if runtime.GOOS == "windows" {
		properties["elevated"] = booleanProp("Request Windows UAC administrator privileges for this command only; defaults to false. The declared effect controls mods approval.")
	}
	return objectSchema(properties, required...)
}
