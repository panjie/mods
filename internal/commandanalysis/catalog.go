package commandanalysis

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"
)

type commandSpace uint8

const (
	nativeSpace commandSpace = iota
	powershellSpace
)

func spaceForShell(posix bool) commandSpace {
	if posix {
		return nativeSpace
	}
	return powershellSpace
}

// InvocationFacts is semantic evidence, never an authorization. Legacy write
// projections remain for compatibility; production authorization uses roles.
type InvocationFacts struct {
	ContextStableRead                                      bool
	Transfer                                               *transferRequest
	EntryID                                                string
	Revision                                               int
	Effect                                                 CommandEffect
	Evidence                                               Evidence
	Coverage                                               TargetCoverage
	Gaps                                                   []AssessmentGap
	Targets                                                []InvocationTarget
	ReadOnly, BuiltinRead, KnownWrite, Risky, BoundedWrite bool
	ReadReason                                             string
	Writes                                                 []writableTarget
	CoverageWrites                                         []writableTarget
	DiagnosticWrite                                        bool
	Git                                                    *CommandAssessment
}

// invocationBinding preserves the parser's distinct evidence strengths. Read
// operands may be dynamic; write discovery may confine scalar substitutions;
// coverage operands must preserve an exact argument boundary. They are inputs
// to one catalog dispatch, never independent command classifications.
type invocationBinding struct {
	Read, Write, Coverage []string
	Name                  string
	NativeProcess         bool
	ExactRead             bool // Read argv was decoded from syntax or supplied by a process call.
}

type TargetRole uint8

const (
	TargetRead TargetRole = iota + 1
	TargetWrite
	TargetCandidate
)

type InvocationTarget struct {
	Literal   bool
	Path      string
	Role      TargetRole
	Operation TargetOperation
}

type readRule func(*commandEntry, []string, ReadOnlyCommandPolicy, *catalogEvaluation) bool
type writeRule func([]string, bool, *catalogEvaluation) ([]writableTarget, bool)
type riskRule func([]string, bool, *catalogEvaluation) bool
type commandEntry struct {
	ReadOperands             func([]string) ([]string, bool) // Separates input files from embedded programs/options.
	NumericOutput            func([]string) bool             // Exact argv produces only a count on stdout.
	LiteralRead              bool                            // Option-sensitive reads require literal argument evidence.
	ScalarOutput             bool
	EnvWrites                bool
	ContextStableRead        bool
	ID, Name, AliasOf        string
	Space                    commandSpace
	Revision                 int
	Compatibility            bool
	Tests                    []string
	Subcommands              []string
	RepositoryWrites         []string
	Options                  []optionSpec
	TextOutput, BoundedWrite bool
	NameOnlyRead             bool
	ShellOnly                bool
	RiskHint                 bool
	ReadDirectory            bool
	ReadPaths                bool
	ReadPathParameters       []string
	FirstReadOperand         bool
	WriteOperation           TargetOperation
	SourceRole               TargetRole
	TransferFile             bool
	TransferMove             bool
	StrictFileOptions        []optionSpec
	StrictFile               bool
	MaxOperands              int
	Read                     readRule
	Write                    writeRule
	Risk                     riskRule
}
type catalogKey struct {
	space commandSpace
	name  string
}
type catalog struct{ entries map[catalogKey]*commandEntry }

var commandCatalog *catalog

func init() { commandCatalog = mustCatalog(builtinCommandEntries()) }

func mustCatalog(entries []commandEntry) *catalog {
	c, err := newCatalog(entries)
	if err != nil {
		panic(err)
	}
	return c
}
func newCatalog(entries []commandEntry) (*catalog, error) {
	c := &catalog{entries: map[catalogKey]*commandEntry{}}
	ids := map[string]bool{}
	for i := range entries {
		e := entries[i]
		e.StrictFileOptions = slices.Clone(e.StrictFileOptions)
		for j := range e.StrictFileOptions {
			e.StrictFileOptions[j].Names = slices.Clone(e.StrictFileOptions[j].Names)
		}
		e.ReadPathParameters = slices.Clone(e.ReadPathParameters)
		e.Tests = slices.Clone(e.Tests)
		e.Subcommands = slices.Clone(e.Subcommands)
		e.RepositoryWrites = slices.Clone(e.RepositoryWrites)
		e.Options = slices.Clone(e.Options)
		for j := range e.Options {
			e.Options[j].Names = slices.Clone(e.Options[j].Names)
		}
		if e.ID == "" || ids[e.ID] || e.Name == "" || e.Revision < 1 || len(e.Tests) == 0 {
			return nil, fmt.Errorf("invalid catalog entry %q", e.ID)
		}
		if e.Space != nativeSpace && e.Space != powershellSpace {
			return nil, fmt.Errorf("invalid identity for %s", e.ID)
		}
		if !isBarePOSIXCommand(e.Name) || e.Space == powershellSpace && normalizePowerShellCommandName(e.Name) != e.Name {
			return nil, fmt.Errorf("non-canonical identity %s", e.ID)
		}
		if e.AliasOf != "" && (e.ReadOperands != nil || e.NumericOutput != nil || e.LiteralRead || e.ScalarOutput || e.EnvWrites || e.ContextStableRead || e.TransferFile || e.TransferMove || e.StrictFile || len(e.StrictFileOptions) > 0 || e.MaxOperands != 0 || e.Read != nil || e.Write != nil || e.Risk != nil || e.TextOutput || e.BoundedWrite || e.NameOnlyRead || e.ShellOnly || e.WriteOperation != 0 || e.SourceRole != 0 || e.ReadDirectory || e.ReadPaths || len(e.ReadPathParameters) > 0 || e.FirstReadOperand || len(e.Options) > 0 || len(e.Subcommands) > 0 || len(e.RepositoryWrites) > 0) {
			return nil, fmt.Errorf("alias contains rules %s", e.ID)
		}
		if e.BoundedWrite && e.Write == nil || e.NameOnlyRead && e.Read == nil || e.TextOutput && (e.Read != nil || e.Write != nil || len(e.Options) == 0) {
			return nil, fmt.Errorf("inconsistent rule %s", e.ID)
		}
		for _, test := range e.Tests {
			if !strings.HasPrefix(test, "Test") {
				return nil, fmt.Errorf("invalid test reference %s", e.ID)
			}
		}
		if _, exists := c.entries[catalogKey{e.Space, e.Name}]; exists {
			return nil, fmt.Errorf("duplicate command identity %s", e.Name)
		}
		if e.ScalarOutput && (e.Space != powershellSpace || e.Write == nil) {
			return nil, fmt.Errorf("invalid scalar writer %s", e.ID)
		}
		if e.EnvWrites && (e.Space != nativeSpace || e.Name != "env") {
			return nil, fmt.Errorf("invalid wrapper %s", e.ID)
		}
		if e.TransferMove && !e.TransferFile || e.TransferFile && (e.Space != nativeSpace || e.Write == nil || e.BoundedWrite) || e.StrictFile && (e.Space != nativeSpace || e.Write == nil) || !e.StrictFile && (len(e.StrictFileOptions) > 0 || e.MaxOperands != 0) {
			return nil, fmt.Errorf("invalid file grammar %s", e.ID)
		}
		if err := validateOptions(e.StrictFileOptions); err != nil {
			return nil, err
		}
		if err := validateOptions(e.Options); err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, sub := range e.Subcommands {
			if sub == "" || strings.ContainsAny(sub, " \t") || strings.ToLower(sub) != sub || seen[sub] {
				return nil, fmt.Errorf("duplicate subcommand in %s", e.ID)
			}
			seen[sub] = true
		}
		for _, sub := range e.RepositoryWrites {
			if sub == "" || strings.ContainsAny(sub, " \t") || strings.ToLower(sub) != sub || seen[sub] {
				return nil, fmt.Errorf("conflicting repository subcommand in %s", e.ID)
			}
			seen[sub] = true
		}
		if e.WriteOperation != 0 && (e.Write == nil || e.WriteOperation < OperationFollowLeaf || e.WriteOperation > OperationDestination) || e.SourceRole != 0 && (e.Write == nil || e.SourceRole != TargetRead && e.SourceRole != TargetWrite) {
			return nil, fmt.Errorf("invalid target roles in %s", e.ID)
		}
		if len(e.Options) > 0 && !e.TextOutput || len(e.Subcommands) > 0 && e.Read == nil || len(e.RepositoryWrites) > 0 && e.Write == nil || e.ShellOnly && e.Space != nativeSpace {
			return nil, fmt.Errorf("unbound grammar in %s", e.ID)
		}
		ids[e.ID] = true
		c.entries[catalogKey{e.Space, e.Name}] = &e
	}
	for _, e := range c.entries {
		if e.AliasOf != "" {
			target := c.entries[catalogKey{e.Space, e.AliasOf}]
			if target == nil || target.AliasOf != "" {
				return nil, fmt.Errorf("invalid alias %s", e.ID)
			}
		}
	}
	return c, nil
}
func (c *catalog) lookup(space commandSpace, name string) *commandEntry {
	e := c.entries[catalogKey{space, name}]
	if e != nil && e.AliasOf != "" {
		return c.entries[catalogKey{space, e.AliasOf}]
	}
	return e
}
func catalogNameOnlyRead(space commandSpace, name string) bool {
	e := commandCatalog.lookup(space, name)
	// A dedicated descriptor identifies compatibility entries whose old grammar
	// accepted runtime read operands. It is not a second command-name table.
	return e != nil && e.Read != nil && e.NameOnlyRead && !e.ShellOnly
}

func catalogProcessTokens(tokens []string, policy ReadOnlyCommandPolicy) InvocationFacts {
	name := ""
	if len(tokens) > 0 {
		name = tokens[0]
	}
	return evaluateInvocation(invocationBinding{Name: name, Read: tokens, Write: tokens, Coverage: tokens, NativeProcess: true, ExactRead: true}, nativeSpace, policy)
}
func catalogTokens(tokens []string, space commandSpace, policy ReadOnlyCommandPolicy) InvocationFacts {
	return newCatalogEvaluation(policy).tokens(tokens, space)
}

type catalogEvaluation struct {
	policy      ReadOnlyCommandPolicy
	memo        map[string]InvocationFacts
	depth       int
	evaluations int
}

func newCatalogEvaluation(policy ReadOnlyCommandPolicy) *catalogEvaluation {
	return &catalogEvaluation{policy: policy, memo: map[string]InvocationFacts{}}
}
func (ev *catalogEvaluation) tokens(tokens []string, space commandSpace) InvocationFacts {
	return ev.boundTokens(tokens, space, false)
}
func (ev *catalogEvaluation) processTokens(tokens []string, space commandSpace) InvocationFacts {
	return ev.boundTokens(tokens, space, true)
}
func (ev *catalogEvaluation) boundTokens(tokens []string, space commandSpace, process bool) InvocationFacts {
	keyBytes, _ := json.Marshal(struct {
		Space   commandSpace
		Args    []string
		Process bool
	}{space, tokens, process})
	key := string(keyBytes)
	if f, ok := ev.memo[key]; ok {
		return f
	}
	name := ""
	if len(tokens) > 0 {
		name = tokens[0]
	}
	f := ev.evaluate(invocationBinding{Name: name, Read: tokens, Write: tokens, Coverage: tokens, NativeProcess: process}, space)
	ev.memo[key] = f
	return f
}
func evaluateInvocation(binding invocationBinding, space commandSpace, policy ReadOnlyCommandPolicy) InvocationFacts {
	return newCatalogEvaluation(policy).evaluate(binding, space)
}
func (ev *catalogEvaluation) evaluate(binding invocationBinding, space commandSpace) InvocationFacts {
	policy := ev.policy
	if ev.depth >= maxAnalysisDepth {
		return InvocationFacts{Effect: EffectUnknown, Coverage: CoverageIncomplete, Gaps: []AssessmentGap{GapAnalysisBudget}}
	}
	ev.evaluations++
	ev.depth++
	defer func() { ev.depth-- }()

	f := InvocationFacts{Effect: EffectUnknown, Coverage: CoverageIncomplete}
	originalSpace := space
	name := binding.Name
	bare := isBarePOSIXCommand(name)
	if space == powershellSpace {
		bare = isBarePowerShellCommand(name)
		raw := strings.ToLower(trimPowerShellLiteral(name))
		name = normalizePowerShellCommandName(name)
		if strings.HasSuffix(raw, ".exe") || strings.HasSuffix(raw, ".cmd") || strings.HasSuffix(raw, ".bat") {
			space = nativeSpace
		}
	} else {
		name = path.Base(name)
	}
	trusted := bare && (space == nativeSpace && policy.matchesPOSIX(name) || space == powershellSpace && policy.matchesPowerShell(name))
	sourceEntry := commandCatalog.entries[catalogKey{space, name}]
	e := commandCatalog.lookup(space, name)
	if e != nil {
		f.EntryID, f.Revision = e.ID, e.Revision
		f.ContextStableRead = e.ContextStableRead
		canonical := func(tokens []string) []string {
			if len(tokens) == 0 {
				return nil
			}
			args := slices.Clone(tokens)
			args[0] = e.Name
			return args
		}
		read, write, coverage := canonical(binding.Read), canonical(binding.Write), canonical(binding.Coverage)
		// Each distinct operand projection is bound once. Usually all three
		// are identical; dynamic syntax keeps weaker discovery separate from
		// complete coverage without repeating identity/entry dispatch.
		type projection struct {
			read, known, risky, bounded bool
			writes                      []writableTarget
			reads                       []InvocationTarget
		}
		project := func(args []string) projection {
			var v projection
			if len(args) == 0 {
				return v
			}
			if e.TextOutput {
				b, ok := bindOptions(e.Options, args[1:])
				if ok && len(b.Operands) <= 2 {
					v.read = !b.Flags["reverse"] && len(b.Operands) < 2 && space == nativeSpace
					v.known = b.Flags["reverse"] || len(b.Operands) == 2
					if len(b.Operands) > 0 {
						v.reads = []InvocationTarget{{Path: b.Operands[0], Role: TargetRead, Operation: OperationRead}}
					}
					if len(b.Operands) == 2 {
						v.writes = []writableTarget{{path: b.Operands[1], mode: writableTargetParent}}
						v.bounded = true
					}
				}
			} else if e.Write != nil {
				v.writes, v.known = e.Write(args, space == nativeSpace, ev)
				v.bounded = e.BoundedWrite && v.known
				if e.ScalarOutput {
					_, _, ok := bindPowerShellOutput(e.Name, args[1:])
					v.bounded = v.bounded && ok
				}
				if e.StrictFile {
					b, ok := bindOptions(e.StrictFileOptions, args[1:])
					if ok { // The binder, including --, is authoritative for exact operands.
						v.writes = parentTargets(b.Operands)
						for _, path := range b.Reads {
							v.reads = append(v.reads, InvocationTarget{Path: path, Role: TargetRead, Operation: OperationRead})
						}
						if e.Name == "rmdir" {
							v.writes = dirTargets(b.Operands)
						}
					}
					v.bounded = v.bounded && !slices.Contains(b.Operands, "-") && ok && len(b.Operands) > 0 && (e.MaxOperands == 0 || len(b.Operands) <= e.MaxOperands)
				}
			}
			if e.Risk != nil {
				v.risky = e.Risk(args, space == nativeSpace, ev)
			}
			hasTarget := false
			for _, w := range v.writes {
				if trimPowerShellLiteral(strings.TrimSpace(w.path)) != "" {
					hasTarget = true
				}
			}
			v.bounded = v.bounded && hasTarget && bare
			return v
		}
		w := project(write)
		c := w
		if !slices.Equal(write, coverage) {
			c = project(coverage)
		}
		r := w
		if !slices.Equal(read, write) {
			if slices.Equal(read, coverage) {
				r = c
			} else {
				r = project(read)
			}
		}
		f.Writes, f.KnownWrite, f.Risky = w.writes, w.known, w.risky || sourceEntry.RiskHint
		f.CoverageWrites, f.BoundedWrite = c.writes, c.bounded
		f.DiagnosticWrite = r.known || r.risky || sourceEntry.RiskHint
		f.Targets = append(f.Targets, r.reads...)
		f.ReadReason = "read-only command: " + name
		if e.TextOutput {
			f.BuiltinRead = r.read
			f.ReadReason = "read-only text output"
		} else if e.Read != nil {
			if len(read) > 0 {
				f.BuiltinRead = e.Read(e, read[1:], ReadOnlyCommandPolicy{}, ev)
			} else {
				f.BuiltinRead = e.NameOnlyRead
			}
			if len(e.Subcommands) > 0 && len(read) > 1 {
				f.ReadReason = "read-only subcommand: " + name + " " + strings.ToLower(read[1])
			}
			// Only env propagates explicit trust to its nested invocation.
			if e.Name == "env" && !f.BuiltinRead && len(policy.Commands) > 0 && len(read) > 0 {
				trusted = trusted || e.Read(e, read[1:], policy, ev)
			}
		}
		// A dynamic operand may prevent a full projection while the command
		// name still proves a write effect. This grants no target coverage.
		if len(write) == 0 && e.Risk != nil {
			f.Risky = e.Risk([]string{e.Name}, space == nativeSpace, ev)
		}
	}
	// Identity correction may narrow a former cmdlet match, but must not
	// introduce new automatic reads during the compatibility migration.
	if originalSpace == powershellSpace && space == nativeSpace {
		previous := commandCatalog.lookup(powershellSpace, name)
		f.BuiltinRead = f.BuiltinRead && previous != nil && previous.Read != nil
	}
	if e != nil && e.ShellOnly && (binding.NativeProcess || originalSpace == powershellSpace) {
		f.BuiltinRead = false
	}
	if e != nil && e.LiteralRead && !binding.ExactRead {
		// Legacy projections have no quoting provenance. Only plain tokens
		// can supply literal evidence; decoded/process argv bypasses this gate.
		for _, arg := range binding.Read {
			if strings.ContainsAny(arg, "$`*?[]\\") {
				f.BuiltinRead = false
				break
			}
		}
	}
	f.BuiltinRead = f.BuiltinRead && bare
	f.ReadOnly = f.BuiltinRead || trusted
	if trusted {
		f.ReadReason = policy.reason(name)
	}
	writes := f.CoverageWrites
	if len(writes) == 0 {
		writes = f.Writes
	}
	for _, w := range writes {
		op := OperationFollowLeaf
		if w.mode == writableTargetDirectory {
			op = OperationDirectory
		}
		if w.mode == writableTargetDestination {
			op = OperationDestination
		}
		if e != nil && e.WriteOperation != 0 {
			op = e.WriteOperation
		}
		f.Targets = append(f.Targets, InvocationTarget{Path: w.path, Role: TargetWrite, Operation: op})
	}
	targetRead := binding.Read
	if len(targetRead) == 0 {
		targetRead = binding.Coverage
	}
	if e != nil && e.ReadOperands != nil && f.BuiltinRead && len(binding.Read) > 0 {
		if inputs, ok := e.ReadOperands(binding.Read[1:]); ok {
			for _, input := range inputs {
				f.Targets = append(f.Targets, InvocationTarget{Path: input, Role: TargetRead, Operation: OperationRead, Literal: true})
			}
		}
	}
	if e != nil && e.SourceRole != 0 && len(targetRead) == 3 && !strings.HasPrefix(targetRead[1], "-") && !strings.HasPrefix(targetRead[2], "-") {
		operands := writeOperands(targetRead[1:], space == nativeSpace)
		if len(operands) > 1 {
			for _, source := range operands[:len(operands)-1] {
				op := OperationRead
				if e.SourceRole == TargetWrite {
					op = OperationRenameEntry
				}
				f.Targets = append(f.Targets, InvocationTarget{Path: source, Role: e.SourceRole, Operation: op})
			}
		}
	} else if len(targetRead) > 1 && len(f.Targets) == 0 && f.BuiltinRead && e != nil && len(e.ReadPathParameters) > 0 {
		for _, source := range powerShellReadPathArguments(psCommandInvocation{Name: e.Name, Args: targetRead[1:]}) {
			f.Targets = append(f.Targets, InvocationTarget{Path: source, Role: TargetRead, Operation: OperationRead})
		}
	} else if len(targetRead) > 1 && len(f.Targets) == 0 && f.BuiltinRead && e != nil && (e.ReadPaths || e.ReadDirectory) {
		role := TargetCandidate
		if f.BuiltinRead && e != nil && (e.ReadPaths || e.ReadDirectory) {
			role = TargetRead
		}
		operands := commandOperands(targetRead[1:])
		op := OperationRead
		if e != nil && e.ReadDirectory {
			operands = findRootOperands(targetRead[1:])
			op = OperationDirectory
		}
		for _, operand := range operands {
			f.Targets = append(f.Targets, InvocationTarget{Path: operand, Role: role, Operation: op})
		}
	}
	if e != nil && e.ScalarOutput && f.BoundedWrite && len(binding.Coverage) > 0 {
		path, literal, ok := bindPowerShellOutput(e.Name, binding.Coverage[1:])
		if ok {
			f.Targets = []InvocationTarget{{Path: path, Role: TargetWrite, Operation: OperationFollowLeaf, Literal: literal}}
		}
	}
	// Reuse exact inner write semantics only for audited env assignments.
	// Host-dependent transfers and dynamic binding remain unproven here.
	if e != nil && e.EnvWrites && bare && originalSpace == nativeSpace && len(binding.Coverage) > 0 {
		if nested, ok := envCommandArgs(binding.Coverage[1:]); ok && len(nested) > 0 {
			inner := ev.processTokens(nested, nativeSpace)
			if inner.BoundedWrite && inner.Evidence == EvidenceStatic && !inner.ReadOnly {
				f.Targets = slices.Clone(inner.Targets)
				f.Writes = slices.Clone(inner.Writes)
				f.CoverageWrites = slices.Clone(inner.CoverageWrites)
				f.BoundedWrite = true
				f.KnownWrite = true
			}
		}
	}
	if e != nil && e.TransferFile && bare && space == nativeSpace {
		b, ok := bindOptions(nil, binding.Coverage[min(1, len(binding.Coverage)):])
		if len(binding.Coverage) > 0 && ok && len(b.Operands) == 2 && b.Operands[0] != "" && b.Operands[1] != "" {
			f.Transfer = &transferRequest{Source: b.Operands[0], Destination: b.Operands[1], Move: e.TransferMove}
		}
	}
	if f.ReadOnly {
		f.Effect, f.Coverage, f.Evidence = EffectRead, CoverageComplete, EvidenceStatic
		if trusted && !f.BuiltinRead {
			f.Evidence = EvidenceTrust
		}
	} else if f.KnownWrite || f.Risky {
		f.Effect, f.Evidence = EffectWrite, EvidenceStatic
		if f.BoundedWrite {
			f.Coverage = CoverageComplete
		} else {
			f.Gaps = []AssessmentGap{GapWriteTargets}
		}
	} else {
		f.Gaps = []AssessmentGap{GapUnknownInvocation}
	}
	return f
}

func readAnyArguments(*commandEntry, []string, ReadOnlyCommandPolicy, *catalogEvaluation) bool {
	return true
}
func readPSForEach(_ *commandEntry, args []string, _ ReadOnlyCommandPolicy, _ *catalogEvaluation) bool {
	// The complete IR still checks every child command, variable and method.
	// A script block alone must not authorize -Parallel, -AsJob, unknown
	// parameters, or the separate Begin/End/RemainingScripts parameter sets.
	if len(args) == 2 && strings.EqualFold(args[0], "-Process") {
		args = args[1:]
	}
	return len(args) == 1 && strings.HasPrefix(strings.TrimSpace(args[0]), "{") && strings.HasSuffix(strings.TrimSpace(args[0]), "}")
}
func readSubcommand(e *commandEntry, args []string, _ ReadOnlyCommandPolicy, _ *catalogEvaluation) bool {
	return readSubcommandEntry(e, args)
}
func readFind(_ *commandEntry, args []string, _ ReadOnlyCommandPolicy, ev *catalogEvaluation) bool {
	return findInvocationReadOnly(args, ev)
}
func readSed(_ *commandEntry, args []string, _ ReadOnlyCommandPolicy, _ *catalogEvaluation) bool {
	return sedInvocationReadOnly(args)
}
func readSort(_ *commandEntry, args []string, _ ReadOnlyCommandPolicy, _ *catalogEvaluation) bool {
	return sortInvocationReadOnly(args)
}
func readXargs(_ *commandEntry, args []string, _ ReadOnlyCommandPolicy, _ *catalogEvaluation) bool {
	return xargsInvocationReadOnly(args)
}
func readEnv(_ *commandEntry, args []string, p ReadOnlyCommandPolicy, ev *catalogEvaluation) bool {
	r, _ := envInvocationReadOnly(args, p, ev)
	return r
}
func alwaysRisky([]string, bool, *catalogEvaluation) bool { return true }
func riskFind(args []string, _ bool, ev *catalogEvaluation) bool {
	return findHasWriteAction(args[1:], ev)
}
func riskSort(args []string, _ bool, _ *catalogEvaluation) bool {
	return sortOutputPath(args[1:]) != "" || sortUsesWritableScratch(args[1:])
}
func riskGit(args []string, _ bool, _ *catalogEvaluation) bool {
	return hasAnyArg(args[1:], "--output", "--ext-diff", "--textconv")
}
func riskXxd(args []string, _ bool, _ *catalogEvaluation) bool {
	return hasAnyArg(args[1:], "-r", "--revert")
}
func riskXargs(args []string, posix bool, ev *catalogEvaluation) bool {
	nested, ok := xargsCommandArgs(args[1:])
	return ok && ev.processTokens(nested, spaceForShell(posix)).Risky
}
func riskEnv(args []string, posix bool, ev *catalogEvaluation) bool {
	nested, ok := envCommandArgs(args[1:])
	return ok && ev.processTokens(nested, spaceForShell(posix)).Risky
}

func catalogHasRead(space commandSpace, name string) bool {
	e := commandCatalog.lookup(space, name)
	return e != nil && e.Read != nil
}
func catalogHasSubcommands(name string) bool {
	e := commandCatalog.lookup(nativeSpace, name)
	return e != nil && len(e.Subcommands) > 0
}
