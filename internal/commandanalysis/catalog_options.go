package commandanalysis

import (
	"fmt"
	"strings"
)

type optionSpec struct {
	Names   []string
	Value   bool
	Flag    string
	Numeric bool
}
type optionBinding struct {
	Operands []string
	Flags    map[string]bool
}

var uniqOptions = []optionSpec{
	{Names: []string{"-c", "-d", "-u", "-i", "-z", "--count", "--repeated", "--unique", "--ignore-case", "--zero-terminated"}},
	{Names: []string{"-f", "-s", "-w", "--skip-fields", "--skip-chars", "--check-chars"}, Value: true, Numeric: true},
}
var xxdOptions = []optionSpec{
	{Names: []string{"-r", "-revert"}, Flag: "reverse"},
	{Names: []string{"-a", "-b", "-e", "-i", "-p", "-ps", "-u"}},
	{Names: []string{"-c", "-cols", "-g", "-groupsize", "-l", "-len", "-s", "-seek", "-o"}, Value: true},
}

func validateOptions(options []optionSpec) error {
	seen := map[string]bool{}
	for _, o := range options {
		if len(o.Names) == 0 || o.Numeric && !o.Value || o.Value && o.Flag != "" {
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

// Only explicitly numeric options accept --name=value or -nVALUE.
// Combined flags and other attached values remain unmodeled.
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
				value := ""
				inline := o.Numeric && ((strings.HasPrefix(name, "--") && strings.HasPrefix(arg, name+"=")) || (len(name) == 2 && len(arg) > 2 && strings.HasPrefix(arg, name)))
				if name == arg || inline {
					found = true
					if inline {
						value = strings.TrimPrefix(arg, name)
						if strings.HasPrefix(name, "--") {
							value = strings.TrimPrefix(value, "=")
						}
					}
					if o.Value && !inline {
						i++
						if i >= len(args) {
							return b, false
						}
						value = args[i]
					}
					if o.Numeric && (value == "" || strings.Trim(value, "0123456789") != "") {
						return b, false
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
