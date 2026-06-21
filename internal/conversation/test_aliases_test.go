package conversation

import "github.com/panjie/mods/internal/approval"

type convoDB = DB
type ApprovalRule = approval.Rule
type approvalRuleSet = approval.RuleSet

var openDB = Open
var newConversationID = NewID
var sha1reg = IDPattern
var errNoMatches = ErrNoMatches
var errManyMatches = ErrManyMatches
var workspaceScope = approval.WorkspaceScope

const (
	sha1short  = ShortIDLength
	sha1minLen = MinIDLength

	approvalShellPrefix = approval.ShellPrefix
	approvalEditAll     = approval.EditAll
)
