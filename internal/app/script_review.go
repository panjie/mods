package app

// Script-execution review: the narrow release valve for interpreter calls whose
// payload is a file instead of inline source.
//
// The command preflight rejects opaque commands, and an interpreter invocation
// is opaque because its effects cannot be bounded. `python script.py` is the one
// shape that can still be reviewed honestly: the reviewer can read exactly the
// bytes the interpreter will run. This file owns that verification. It proves
// the operand is a readable UTF-8 text file inside the workspace or a safe
// directory, binds the digest of the reviewed bytes, and re-checks that digest
// immediately before execution.
//
// Everything else stays opaque: inline `-c`/`-e`, encoded payloads, nested shell
// hosts, path-qualified interpreters, extra arguments, and scripts outside the
// workspace are still rejected by the preflight.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/pathutil"
)

// maxScriptReviewBytes bounds the script that may enter review. The reviewer
// has to page through the whole content before approving, so the cap keeps a
// reviewed script humanly completable instead of turning approval into
// scroll-to-the-bottom.
const maxScriptReviewBytes = 128 << 10

var (
	errScriptReviewUnavailable = errors.New("script could not be verified for review and was not run; it must be a readable UTF-8 text file inside the workspace or a temporary directory")
	errScriptReviewChanged     = errors.New("script changed after review and was not run; re-read the file and call again so the reviewed content matches what executes")
)

// verifyScriptExecution binds a literal interpreter script operand to concrete
// bytes when, and only when, the operand resolves to a reviewable text file
// inside the workspace or a safe directory. Anything else is left unverified and
// therefore still opaque to the preflight.
func (m *Mods) verifyScriptExecution(result approval.CommandAssessment, cwd string, flavor pathutil.Flavor) approval.CommandAssessment {
	facts := result.Reviewability.ScriptExecution
	if facts == nil {
		return result
	}
	if result.StaticRead {
		// A deterministic read verdict — including an explicit user read-only
		// policy — keeps its existing no-review exemption.
		return result
	}
	// An interpreter's effects are unbounded, so a classifier's read verdict
	// must not lift the script payload into the always-allowed read cell, and a
	// guessed target must not advertise a bounded scope for the review. Only the
	// content displayed for this call may approve it.
	result.Effect = approval.EffectUnknown
	result.StaticRead = false
	result.KnownDirs = nil
	result.Reason = "script execution: effects cannot be proven"
	workspace := ""
	if m != nil && m.Config != nil {
		workspace = m.Config.ResolveWorkspace().Canonical
	}
	safe := m.safeDirs()
	insideBoundary := func(path string) bool {
		switch pathutil.Location(path, workspace, safe) {
		case pathutil.LocationWorkspace:
			return workspace != ""
		case pathutil.LocationSafe:
			return true
		default:
			return false
		}
	}
	base := strings.TrimSpace(cwd)
	if base == "" {
		base = workspace
	}
	resolved := strings.TrimSpace(facts.Operand)
	if !pathutil.IsAbs(resolved) {
		resolved = pathutil.NormalizeShellPath(resolved, pathutil.DefaultOptions(base, flavor))
	}
	if resolved == "" || !insideBoundary(resolved) {
		return result
	}
	// The lexical path is inside the boundary; a symlinked leaf must not carry
	// the read outside it.
	real, err := filepath.EvalSymlinks(resolved)
	if err != nil || !insideBoundary(real) {
		return result
	}
	content, err := readReviewableScript(resolved)
	if err != nil {
		return result
	}
	facts.ResolvedPath = resolved
	facts.SizeBytes = int64(len(content))
	facts.ContentSHA256 = scriptDigest(content)
	result.Reason = "script execution: one interpreter and one literal script path"
	return result
}

// scriptReviewSource returns the script body the reviewer must see and refreshes
// the bound digest so approval covers exactly the displayed bytes. When the file
// cannot be read the digest is cleared, which makes the call unverifiable and
// therefore unexecutable.
func scriptReviewSource(facts *approval.ScriptExecutionFacts) (string, bool) {
	if facts == nil || facts.ResolvedPath == "" {
		return "", false
	}
	content, err := readReviewableScript(facts.ResolvedPath)
	if err != nil {
		facts.ContentSHA256 = ""
		facts.SizeBytes = 0
		return "", false
	}
	facts.SizeBytes = int64(len(content))
	facts.ContentSHA256 = scriptDigest(content)
	return string(content), true
}

// verifyReviewedScript refuses to run an interpreter script whose bytes no
// longer match the content the reviewer approved. The window between this check
// and the interpreter opening the file is not closed here: executing a snapshot
// copy would change the interpreter's view of its own path, so this design
// verifies the file instead and documents the residual race.
func verifyReviewedScript(assessment *approval.CommandAssessment) error {
	if assessment == nil {
		return nil
	}
	facts := assessment.Reviewability.ScriptExecution
	if facts == nil || facts.ResolvedPath == "" {
		return nil
	}
	if !facts.Verified() {
		return errScriptReviewUnavailable
	}
	content, err := readReviewableScript(facts.ResolvedPath)
	if err != nil {
		return errScriptReviewUnavailable
	}
	if int64(len(content)) != facts.SizeBytes || scriptDigest(content) != facts.ContentSHA256 {
		return errScriptReviewChanged
	}
	return nil
}

// readReviewableScript applies the reviewability rules that make a script body
// presentable and comparable: a bounded, NUL-free, valid UTF-8 regular file.
func readReviewableScript(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	if info.Size() > maxScriptReviewBytes {
		return nil, fmt.Errorf("script is larger than the review limit")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(content) > maxScriptReviewBytes {
		return nil, fmt.Errorf("script grew beyond the review limit")
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return nil, fmt.Errorf("script is not text")
	}
	if !utf8.Valid(content) {
		return nil, fmt.Errorf("script is not valid UTF-8")
	}
	return content, nil
}

// appendScriptReviewRows adds the verified script shape to a shell or process
// review: the resolved path, the size, the digest of the displayed bytes, and
// the complete source. Shell and process reviews are already paginated, so
// approval stays withheld until every page has been shown.
func appendScriptReviewRows(rows []interactionRow, assessment approval.CommandAssessment) []interactionRow {
	facts := assessment.Reviewability.ScriptExecution
	if !facts.Verified() {
		return rows
	}
	rows = append(rows, interactionRow{Label: "Script", Value: facts.ResolvedPath})
	body, ok := scriptReviewSource(facts)
	if !ok {
		return append(rows, interactionRow{
			Label: "Script source",
			Value: "unavailable: the file changed or is no longer readable, so this call cannot be approved",
		})
	}
	return append(rows,
		interactionRow{Label: "Script size", Value: fmt.Sprintf("%d bytes", facts.SizeBytes)},
		interactionRow{Label: "Script SHA-256", Value: shortScriptDigest(facts.ContentSHA256)},
		interactionRow{Label: "Script source (complete)", Value: body},
	)
}

func scriptDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// shortScriptDigest keeps the displayed fingerprint readable in the review
// panel. Humans compare it against a re-read; the full digest stays in memory.
func shortScriptDigest(digest string) string {
	const shown = 16
	if len(digest) <= shown {
		return digest
	}
	return digest[:shown]
}
