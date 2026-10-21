//go:build windows && windowsreliability

package tools

import "testing"

// Select the ordinary regression tests in the PowerShell 5.1/7 reliability
// lanes without running them twice in the default Windows suite.
func TestWindowsReliabilityLiteralShellCwd(t *testing.T)     { TestShellLiteralCwd(t) }
func TestWindowsReliabilityStructuredDownloads(t *testing.T) { TestDownloadsBatchAndAuthorization(t) }
