package app

import (
	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/skills"
)

func (m *Mods) skillSafeDirs() []string {
	if m == nil {
		return nil
	}
	return skills.SafeDirs(m.skillCatalog)
}

func (m *Mods) safeDirs() []string {
	return approval.SafeDirsWith(m.skillSafeDirs())
}
