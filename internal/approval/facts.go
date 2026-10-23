package approval

func (intent AccessIntent) HasCompleteWriteTargets() bool {
	return intent.WriteCoverage == CoverageComplete && len(intent.Gaps) == 0 &&
		!intent.HasUnresolvedPaths() && !intent.HasUnresolvedRemoteTargets() && !intent.HasProviderWriteTargets()
}
