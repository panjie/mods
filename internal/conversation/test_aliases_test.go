package conversation

import "github.com/charmbracelet/mods/internal/approval"

type convoDB = DB
type ApprovalRule = approval.Rule

var openDB = Open
var newConversationID = NewID
var sha1reg = IDPattern
var errNoMatches = ErrNoMatches
var errManyMatches = ErrManyMatches

const (
	sha1short  = ShortIDLength
	sha1minLen = MinIDLength

	approvalShellPrefix = approval.ShellPrefix
	approvalEditAll     = approval.EditAll
)
