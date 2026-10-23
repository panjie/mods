package commandtrace

import "testing"

func TestCountersAreOptIn(t *testing.T) {
	before := Snapshot()
	POSIXParse()
	BridgeParse()
	BridgeRoundTrip()
	GitQuery()
	want := Counts{}
	if Enabled {
		want = Counts{1, 1, 1, 1}
	}
	if got := Snapshot().Since(before); got != want {
		t.Fatalf("counter delta = %+v, want %+v", got, want)
	}
}
