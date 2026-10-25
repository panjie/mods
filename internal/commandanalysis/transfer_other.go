//go:build !linux && !darwin

package commandanalysis

import "os"

// A native-port identity/volume proof has not been audited on these platforms.
func transferFileIdentity(os.FileInfo) (uint64, uint64, bool) { return 0, 0, false }
