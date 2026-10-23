//go:build !commandtrace

package commandtrace

const Enabled = false

func POSIXParse()      {}
func BridgeParse()     {}
func BridgeRoundTrip() {}
func GitQuery()        {}
func Snapshot() Counts { return Counts{} }
