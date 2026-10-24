package commandanalysis

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/panjie/mods/internal/commandtrace"
	"github.com/panjie/mods/internal/pathutil"
	"mvdan.cc/sh/v3/syntax"
)

// parsedCommand belongs to one Analyze call. All consumers traverse the same
// immutable syntax; no parser or filesystem result survives revalidation.
type parsedCommand struct {
	semantics *semanticSession
	source    string
	file      *syntax.File
	ir        *psBridgeIR
	err       error
	nodes     []parsedNode
	nodeIDs   map[syntax.Node]int
}

type parsedNode struct {
	ID, Parent int
	Start, End uint
	Kind       string
}

func parsePOSIX(source string) (*syntax.File, error) {
	commandtrace.POSIXParse()
	return syntax.NewParser(syntax.Variant(syntax.LangPOSIX)).Parse(strings.NewReader(source), "")
}

func parseCommandLegacy(source string, posix bool) *parsedCommand {
	flavor := pathutil.FlavorPOSIX
	if !posix {
		flavor = pathutil.FlavorPowerShell
	}
	return NewEngine(Settings{}, Dependencies{}).run(context.Background()).parse(source, flavor)
}

func (r *analysisRun) parse(source string, flavor pathutil.Flavor) *parsedCommand {
	p := &parsedCommand{source: source, nodeIDs: map[syntax.Node]int{}}
	if len(source) > maxAnalysisInputBytes {
		p.err = errAnalysisBudget
		r.failure = p.err
		return p
	}
	if err := r.ctx.Err(); err != nil {
		p.err = err
		return p
	}
	if flavor == pathutil.FlavorPOSIX {
		p.file, p.err = parsePOSIX(source)
		if p.err == nil {
			stack := []int{}
			syntax.Walk(p.file, func(n syntax.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				if p.err != nil || len(p.nodes) >= maxAnalysisNodes || len(stack) >= maxAnalysisDepth {
					p.err = errAnalysisBudget
					return false
				}
				parent := 0
				if len(stack) > 0 {
					parent = stack[len(stack)-1]
				}
				id := len(p.nodes) + 1
				p.nodeIDs[n] = id
				p.nodes = append(p.nodes, parsedNode{ID: id, Parent: parent, Start: n.Pos().Offset(), End: n.End().Offset(), Kind: fmt.Sprintf("%T", n)})
				stack = append(stack, id)
				return true
			})
		}
	} else {
		if r.dependencies.PowerShell != nil {
			var raw []byte
			raw, p.err = r.dependencies.PowerShell.Parse(r.ctx, source)
			if len(raw) > maxBridgeOutputBytes {
				p.err = errAnalysisBudget
			}
			if p.err == nil {
				p.ir, p.err = unmarshalBridgeResponse(raw)
			}
		} else {
			p.ir, p.err = parseWithBridgeHost(r.ctx, source, r.environment)
		}
		if p.err == nil {
			p.err = validateIRNodes(p.ir, source)
		}
		if p.err == nil && len(p.ir.ParseErrors) > 0 {
			p.err = errors.New("PowerShell parse error")
		}
	}
	if errors.Is(p.err, errAnalysisBudget) {
		r.failure = p.err
	}
	return p
}

const (
	maxAnalysisInputBytes = 1 << 20
	maxAnalysisNodes      = 100000
	maxAnalysisDepth      = 256
)

var errAnalysisBudget = errors.New("command analysis budget exceeded")

func validateIRNodes(ir *psBridgeIR, source string) error {
	if len(ir.Nodes) > maxAnalysisNodes {
		return errAnalysisBudget
	}
	if len(ir.Nodes) == 0 && len(ir.ParseErrors) == 0 {
		return errors.New("missing PowerShell syntax nodes")
	}
	units := 0
	for _, r := range source {
		units++
		if r > 0xffff {
			units++
		}
	}
	depth := map[int]int{0: 0}
	for _, n := range ir.Nodes {
		d, ok := depth[n.Parent]
		if !ok || n.ID <= 0 || n.Parent >= n.ID || n.Start < 0 || n.End < n.Start || n.End > units {
			return errors.New("invalid PowerShell node relationship")
		}
		if _, exists := depth[n.ID]; exists {
			return errors.New("duplicate PowerShell node")
		}
		if d >= maxAnalysisDepth {
			return errAnalysisBudget
		}
		depth[n.ID] = d + 1
	}
	for _, inv := range ir.Invocations {
		if _, ok := depth[inv.NodeID]; !ok || inv.NodeID == 0 {
			return errors.New("invalid PowerShell invocation node")
		}
		for _, arg := range inv.Arguments {
			if _, ok := depth[arg.NodeID]; !ok || arg.NodeID == 0 {
				return errors.New("invalid PowerShell argument node")
			}
		}
	}
	for _, redir := range ir.Redirections {
		if _, ok := depth[redir.NodeID]; !ok || redir.NodeID == 0 {
			return errors.New("invalid PowerShell redirect node")
		}
		if _, ok := depth[redir.Owner]; !ok || redir.Owner == 0 {
			return errors.New("invalid PowerShell redirect owner")
		}
	}
	return nil
}
