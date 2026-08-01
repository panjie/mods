package app

import (
	"github.com/panjie/mods/internal/approval"
)

func (*Mods) safeDirs() []string {
	return approval.SafeDirs()
}
