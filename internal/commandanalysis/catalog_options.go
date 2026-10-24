package commandanalysis

import (
	"fmt"
	"strings"
)

type optionSpec struct {
	Names []string
	Value bool
	Flag  string
}
type optionBinding struct {
	Operands []string
	Flags    map[string]bool
}

var uniqOptions = []optionSpec{
	{Names: []string{"-c", "-d", "-u", "-i", "-z", "--count", "--repeated", "--unique", "--ignore-case", "--zero-terminated"}},
	{Names: []string{"-f", "-s", "-w", "--skip-fields", "--skip-chars", "--check-chars"}, Value: true},
}
var xxdOptions = []optionSpec{
	{Names: []string{"-r", "-revert"}, Flag: "reverse"},
	{Names: []string{"-a", "-b", "-e", "-i", "-p", "-ps", "-u"}},
	{Names: []string{"-c", "-cols", "-g", "-groupsize", "-l", "-len", "-s", "-seek", "-o"}, Value: true},
}

func validateOptions(options []optionSpec) error {
	seen := map[string]bool{}
	for _, o := range options {
		if len(o.Names) == 0 || o.Value && o.Flag != "" {
			return fmt.Errorf("empty option names")
		}
		for _, name := range o.Names {
			if name == "-" || name == "--" || strings.ContainsAny(name, " =\t") || !strings.HasPrefix(name, "-") || seen[name] {
				return fmt.Errorf("conflicting option %s", name)
			}
			seen[name] = true
		}
	}
	return nil
}

// This grammar deliberately does not infer combined flags or --name=value.
// Tokens after -- and a lone - are operands, including leading-dash filenames.
func bindOptions(options []optionSpec, args []string) (optionBinding, bool) {
	b := optionBinding{Flags: map[string]bool{}}
	parse := true
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if parse && arg == "--" {
			parse = false
			continue
		}
		if !parse || arg == "-" || !strings.HasPrefix(arg, "-") {
			b.Operands = append(b.Operands, arg)
			continue
		}
		found := false
		for _, o := range options {
			for _, name := range o.Names {
				if name == arg {
					found = true
					if o.Value {
						i++
						if i >= len(args) {
							return b, false
						}
					}
					if o.Flag != "" {
						b.Flags[o.Flag] = true
					}
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return b, false
		}
	}
	return b, true
}
