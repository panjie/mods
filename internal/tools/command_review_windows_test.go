//go:build windows

package tools

import "testing"

// Selected by the existing PowerShell 5.1/7 reliability lanes as well as the
// ordinary Windows test suite. These tests execute real local tools.
func TestWindowsReliabilityReviewedScript(t *testing.T)      { TestScriptExecutesSourceSnapshot(t) }
func TestWindowsReliabilityLiteralShellCwd(t *testing.T)     { TestShellLiteralCwd(t) }
func TestWindowsReliabilityStructuredDownloads(t *testing.T) { TestDownloadsBatchAndAuthorization(t) }
