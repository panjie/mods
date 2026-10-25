package commandanalysis

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/panjie/mods/internal/pathutil"
)

// TransferResolver is a read-only, bounded proof of an ordinary file transfer.
// No command is executed. Implementations reject links, special files,
// missing parents and different filesystems instead of guessing the effect.
type TransferResolver interface {
	ResolveTransfer(context.Context, string, string) (TransferPaths, error)
}
type TransferPaths struct{ Source, Destination string }
type transferRequest struct {
	Source, Destination string
	Move                bool
}

func (hostPaths) ResolveTransfer(ctx context.Context, source, destination string) (TransferPaths, error) {
	fail := func() (TransferPaths, error) { return TransferPaths{}, errors.New("unmodeled file transfer") }
	if err := ctx.Err(); err != nil {
		return TransferPaths{}, err
	}
	src, err := os.Lstat(source)
	if err != nil || !src.Mode().IsRegular() {
		return fail()
	}
	dev, links, ok := transferFileIdentity(src)
	if !ok || links != 1 {
		return fail()
	}
	// Resolve through all existing components, then require lexical identity.
	// Following a link would change the command's source/destination semantics.
	real, err := pathutil.ResolveThroughExistingParent(source)
	if err != nil || real != source {
		return fail()
	}
	dst, err := os.Lstat(destination)
	if err != nil && !os.IsNotExist(err) {
		return fail()
	}
	if err == nil && dst.IsDir() {
		destination = filepath.Join(destination, filepath.Base(source))
		dst, err = os.Lstat(destination)
	}
	if err != nil && !os.IsNotExist(err) {
		return fail()
	}
	if err == nil {
		dstDev, dstLinks, known := transferFileIdentity(dst)
		if !dst.Mode().IsRegular() || !known || dstDev != dev || dstLinks != 1 || os.SameFile(src, dst) {
			return fail()
		}
	}
	parent, err := os.Stat(filepath.Dir(destination))
	if err != nil || !parent.IsDir() {
		return fail()
	}
	parentDev, _, ok := transferFileIdentity(parent)
	if !ok || parentDev != dev {
		return fail()
	}
	real, err = pathutil.ResolveThroughExistingParent(destination)
	if err != nil || real != destination {
		return fail()
	}
	if err := ctx.Err(); err != nil {
		return TransferPaths{}, err
	}
	return TransferPaths{Source: source, Destination: destination}, nil
}
func (r *analysisRun) resolveTransferFacts(f *InvocationFacts, cwd string, literal bool) {
	if r == nil || f.Transfer == nil || f.ReadOnly || cwd == "" {
		return
	}
	req := f.Transfer
	normalize := func(raw string) (string, bool) {
		// This first tranche requires exact literal operands, not environment,
		// globbing, substitutions or destination-type guesses.
		if !literal && strings.ContainsAny(raw, "$`*?[]~") {
			return "", false
		}
		if strings.HasSuffix(raw, "/") || strings.HasSuffix(raw, `\`) {
			return "", false
		}
		for _, component := range strings.Split(raw, "/") {
			if component == ".." {
				return "", false
			}
		}
		p := normalizeLiteralProcessPath(raw, cwd, pathutil.FlavorPOSIX)
		return p, filepath.IsAbs(p)
	}
	source, ok := normalize(req.Source)
	if !ok {
		return
	}
	destination, ok := normalize(req.Destination)
	if !ok {
		return
	}
	resolver, ok := r.dependencies.Paths.(TransferResolver)
	if !ok {
		return
	}
	if err := r.beginQuery(); err != nil {
		return
	}
	paths, err := resolver.ResolveTransfer(r.ctx, source, destination)
	if errors.Is(err, errAnalysisBudget) {
		r.failure = err
	}
	if err != nil {
		return
	}
	if err := r.accountOutput(len(paths.Source) + len(paths.Destination)); err != nil {
		return
	}
	if paths.Source != source || !filepath.IsAbs(paths.Destination) || paths.Destination != destination && paths.Destination != filepath.Join(destination, filepath.Base(source)) {
		return
	}
	sourceRole, sourceOp, destOp := TargetRead, OperationRead, OperationFollowLeaf
	if req.Move {
		sourceRole, sourceOp, destOp = TargetWrite, OperationRenameEntry, OperationRenameEntry
	}
	f.Targets = []InvocationTarget{{Path: paths.Source, Role: sourceRole, Operation: sourceOp}, {Path: paths.Destination, Role: TargetWrite, Operation: destOp}}
	f.Writes = parentTargets([]string{paths.Destination})
	if req.Move {
		f.Writes = append(f.Writes, parentTargets([]string{paths.Source})...)
	}
	f.CoverageWrites = f.Writes
	f.BoundedWrite, f.KnownWrite = true, true
	f.Effect, f.Evidence, f.Coverage, f.Gaps = EffectWrite, EvidenceStatic, CoverageComplete, nil
}
