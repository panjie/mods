//go:build commandtrace

package commandtrace

import "sync/atomic"

const Enabled = true

var posix, bridge, trips, git atomic.Uint64

func POSIXParse()      { posix.Add(1) }
func BridgeParse()     { bridge.Add(1) }
func BridgeRoundTrip() { trips.Add(1) }
func GitQuery()        { git.Add(1) }
func Snapshot() Counts { return Counts{posix.Load(), bridge.Load(), trips.Load(), git.Load()} }
