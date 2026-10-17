// Command score evaluates one saved mods session against the diagnosis rubric.
//
// The session database is copied to a temporary directory before it is opened,
// so scoring never migrates or writes the live database.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/panjie/mods/internal/evals"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/session"
)

var errRubricFailed = errors.New("rubric failed")

func main() {
	if err := run(); err != nil {
		if errors.Is(err, errRubricFailed) {
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "score:", err)
		os.Exit(2)
	}
}

func run() error {
	dbPath := flag.String("db", "", "path to the mods session database (required)")
	sessionRef := flag.String("session", "", "session id prefix or exact title (required)")
	caseID := flag.String("case", evals.CaseAmbiguousSymptom, "case id: ambiguous-symptom, specific-artifact, or expected-probe-value")
	target := flag.String("target", "", "live artifact path for specific-artifact")
	token := flag.String("expect-token", "", "expected probe value for expected-probe-value")
	asJSON := flag.Bool("json", false, "print the report as JSON")
	strict := flag.Bool("strict", false, "exit 1 when a dimension fails")
	flag.Parse()

	if *dbPath == "" || *sessionRef == "" {
		return errors.New("-db and -session are required")
	}
	caseDef, ok := evals.CaseByID(*caseID)
	if !ok {
		return fmt.Errorf("unknown case %q", *caseID)
	}
	if *target != "" {
		caseDef.TargetPath = *target
	}
	if *token != "" {
		caseDef.ExpectedProbeToken = *token
	}
	if caseDef.ID == evals.CaseSpecificArtifact && caseDef.TargetPath == "" {
		return errors.New("specific-artifact requires -target")
	}
	if caseDef.ID == evals.CaseExpectedProbe && caseDef.ExpectedProbeToken == "" {
		return errors.New("expected-probe-value requires -expect-token")
	}

	copyPath, cleanup, err := copySessionDatabase(*dbPath)
	if err != nil {
		return err
	}
	defer cleanup()

	db, err := session.Open(copyPath)
	if err != nil {
		return err
	}
	defer db.Close()

	found, err := db.Find(*sessionRef)
	if err != nil {
		return err
	}
	var messages []proto.Message
	if err := db.ReadMessages(found.ID, &messages); err != nil {
		return err
	}

	report := evals.Score(messages, caseDef)
	if *asJSON {
		if err := writeJSON(os.Stdout, found, caseDef, report); err != nil {
			return err
		}
	} else {
		writeText(os.Stdout, found, caseDef, report)
	}
	if *strict && !report.Passed() {
		return errRubricFailed
	}
	return nil
}

func writeText(w io.Writer, found *session.Session, caseDef evals.Case, report evals.Report) {
	firstTool := report.FirstTool
	if firstTool == "" {
		firstTool = "(none)"
	}
	fmt.Fprintf(w, "session: %s %q\n", found.ID, found.Title)
	fmt.Fprintf(w, "case:    %s - %s\n", caseDef.ID, caseDef.Description)
	fmt.Fprintf(w, "run:     %d user turn(s), %d tool call(s), first tool %s\n", report.UserTurns, report.ToolCalls, firstTool)
	for _, dimension := range report.Dimensions {
		status := "PASS"
		if !dimension.Passed {
			status = "FAIL"
		}
		fmt.Fprintf(w, "%s %-16s %s\n", status, dimension.Name, dimension.Evidence)
	}
	if len(report.Notes) > 0 {
		fmt.Fprintln(w, "notes:")
		for _, note := range report.Notes {
			fmt.Fprintf(w, "  - %s\n", note)
		}
	}
	result := "FAIL"
	if report.Passed() {
		result = "PASS"
	}
	fmt.Fprintf(w, "result:  %s (%d/%d)\n", result, report.PassedCount(), report.Total())
}

func writeJSON(w io.Writer, found *session.Session, caseDef evals.Case, report evals.Report) error {
	payload := struct {
		Session struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"session"`
		Case   evals.Case   `json:"case"`
		Report evals.Report `json:"report"`
	}{Case: caseDef, Report: report}
	payload.Session.ID = found.ID
	payload.Session.Title = found.Title
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
}

// copySessionDatabase copies the database and any WAL companions so the scorer
// can run migrations on the copy without touching the live database.
func copySessionDatabase(path string) (copyPath string, cleanup func(), err error) {
	if _, err := os.Stat(path); err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "mods-eval-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	destination := filepath.Join(dir, "mods.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		source := path + suffix
		if _, err := os.Stat(source); err != nil {
			if os.IsNotExist(err) && suffix != "" {
				continue
			}
			cleanup()
			return "", nil, err
		}
		if err := copyFile(source, destination+suffix); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	return destination, cleanup, nil
}

func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
