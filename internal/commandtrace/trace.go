// Package commandtrace provides opt-in, content-free instrumentation for command
// analysis tests. It has no role in classification or authorization.
package commandtrace

type Counts struct {
	POSIXParses      uint64 `json:"posix_parses"`
	BridgeParses     uint64 `json:"bridge_parses"`
	BridgeRoundTrips uint64 `json:"bridge_round_trips"`
	GitQueries       uint64 `json:"git_queries"`
}

func (c Counts) Since(before Counts) Counts {
	return Counts{c.POSIXParses - before.POSIXParses, c.BridgeParses - before.BridgeParses, c.BridgeRoundTrips - before.BridgeRoundTrips, c.GitQueries - before.GitQueries}
}
