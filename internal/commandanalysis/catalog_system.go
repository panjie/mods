package commandanalysis

import "strings"

// These two compatibility entries previously accepted system mutations by
// name alone. Only explicit display forms are evidence of a read; setting the
// clock or hostname and unknown forms require ordinary review.
func readHostname(_ *commandEntry, args []string, _ ReadOnlyCommandPolicy, _ *catalogEvaluation) bool {
	if len(args) == 0 {
		return true
	}
	if len(args) != 1 {
		return false
	}
	switch args[0] {
	case "-a", "--alias", "-A", "--all-fqdns", "-d", "--domain", "-f", "--fqdn", "--long", "-i", "--ip-address", "-I", "--all-ip-addresses", "-s", "--short", "--help", "--version":
		return true
	}
	return false
}
func readDate(_ *commandEntry, args []string, _ ReadOnlyCommandPolicy, _ *catalogEvaluation) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "+") {
			continue
		}
		switch a {
		case "-u", "--utc", "--universal", "-R", "--rfc-email", "-I", "-Idate", "-Ihours", "-Iminutes", "-Iseconds", "-Ins", "--iso-8601", "--help", "--version":
		case "-d", "--date", "-r", "--reference":
			i++
			if i >= len(args) {
				return false
			}
		default:
			name, _, ok := strings.Cut(a, "=")
			if !ok || name != "--date" && name != "--reference" {
				return false
			}
		}
	}
	return true
}
