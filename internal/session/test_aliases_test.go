package session

import "github.com/panjie/mods/internal/approval"

type sessionDB = DB
type ApprovalRule = approval.Rule
type approvalRuleSet = approval.RuleSet
type AccessClass = approval.AccessClass

var openDB = Open
var newSessionID = NewID
var sha1reg = IDPattern
var errNoMatches = ErrNoMatches
var errManyMatches = ErrManyMatches
var workspaceScope = approval.WorkspaceScope
var rulesAllowDirs = approval.RulesAllowDirs

const (
	sha1short  = ShortIDLength
	sha1minLen = MinIDLength

	approvalShellPrefix = approval.ShellPrefix
	approvalEditAll     = approval.EditAll
	approvalDirAllow    = approval.DirAllow

	accessRead  = approval.AccessRead
	accessWrite = approval.AccessWrite
)
