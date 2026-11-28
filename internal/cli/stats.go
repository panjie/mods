package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/panjie/mods/internal/proto"
)

func printInteractionStats(mods *Mods) {
	if !config.ShowTokenUsage || mods == nil {
		return
	}
	stats := mods.Stats()
	if IsErrorTTY() {
		_, _ = lipgloss.Fprintln(os.Stderr, "\n"+styledInteractionStats(stats, StderrStyles()))
		return
	}
	fmt.Fprintln(os.Stderr, formatInteractionStats(stats))
}

func formatInteractionStats(stats InteractionStats) string {
	lines := []string{tokenUsageLine(stats.Usage, stats.ModelDuration)}
	if timeLine := timeStatsLine(stats); timeLine != "" {
		lines = append(lines, timeLine)
	}
	if runLine := runStatsLine(stats); runLine != "" {
		lines = append(lines, runLine)
	}
	return strings.Join(lines, "\n")
}

func styledInteractionStats(stats InteractionStats, styles Styles) string {
	lines := []string{styledTokenUsageLine(stats.Usage, stats.ModelDuration, styles)}
	if timeLine := styledTimeStatsLine(stats, styles); timeLine != "" {
		lines = append(lines, timeLine)
	}
	if runLine := styledRunStatsLine(stats, styles); runLine != "" {
		lines = append(lines, runLine)
	}
	return strings.Join(lines, "\n")
}

func tokenUsageLine(usage proto.TokenUsage, modelDuration time.Duration) string {
	if !usage.Available() {
		return "Token usage: unavailable"
	}
	parts := []string{
		"input " + formatTokenCount(usage.InputTokens),
		"output " + formatTokenCount(usage.OutputTokens),
		"total " + formatTokenCount(usage.TotalTokens),
	}
	if usage.CachedInputTokens != 0 {
		parts = append(parts, "cached input "+formatTokenCount(usage.CachedInputTokens))
	}
	if usage.ReasoningOutputTokens != 0 {
		parts = append(parts, "reasoning output "+formatTokenCount(usage.ReasoningOutputTokens))
	}
	if rate := outputTokensPerSecond(usage, modelDuration); rate != "" {
		parts = append(parts, rate+" tok/s")
	}
	return "Token usage: " + strings.Join(parts, ", ")
}

func styledTokenUsageLine(usage proto.TokenUsage, modelDuration time.Duration, styles Styles) string {
	if !usage.Available() {
		return styles.Comment.Render("  Tokens  unavailable")
	}
	segments := []string{
		styledStatPair(formatTokenCount(usage.InputTokens), "input", styles),
		styledStatPair(formatTokenCount(usage.OutputTokens), "output", styles),
		styledStatPair(formatTokenCount(usage.TotalTokens), "total", styles),
	}
	if usage.CachedInputTokens != 0 {
		segments = append(segments, styledStatPair(formatTokenCount(usage.CachedInputTokens), "cached input", styles))
	}
	if usage.ReasoningOutputTokens != 0 {
		segments = append(segments, styledStatPair(formatTokenCount(usage.ReasoningOutputTokens), "reasoning output", styles))
	}
	if rate := outputTokensPerSecond(usage, modelDuration); rate != "" {
		segments = append(segments, styledStatPair(rate, "tok/s", styles))
	}
	return styles.Comment.Render("  Tokens  ") + strings.Join(segments, styles.Comment.Render("  ·  "))
}

func hasTimeStats(stats InteractionStats) bool {
	return stats.Duration > 0 ||
		stats.ModelDuration > 0 ||
		stats.Tools.Duration > 0 ||
		stats.ReviewDuration > 0 ||
		stats.FirstResponse > 0 ||
		stats.ModelRounds > 0 ||
		stats.Tools.Total > 0
}

func timeStatsLine(stats InteractionStats) string {
	if !hasTimeStats(stats) {
		return ""
	}
	parts := []string{formatStatsDuration(stats.Duration)}
	if stats.ModelDuration > 0 && (stats.Tools.Total > 0 || stats.ReviewDuration > 0 || stats.Compactions > 0) {
		parts = append(parts, "model "+formatStatsDuration(stats.ModelDuration))
	}
	if stats.Tools.Total > 0 {
		parts = append(parts, "tools "+formatStatsDuration(stats.Tools.Duration))
	}
	if stats.ReviewDuration > 0 {
		parts = append(parts, "review "+formatStatsDuration(stats.ReviewDuration))
	}
	if stats.FirstResponse > 0 {
		parts = append(parts, "first response "+formatStatsDuration(stats.FirstResponse))
	}
	return "Time: " + strings.Join(parts, ", ")
}

func styledTimeStatsLine(stats InteractionStats, styles Styles) string {
	if !hasTimeStats(stats) {
		return ""
	}
	segments := []string{
		styledStatPair(formatStatsDuration(stats.Duration), "total", styles),
	}
	if stats.ModelDuration > 0 && (stats.Tools.Total > 0 || stats.ReviewDuration > 0 || stats.Compactions > 0) {
		segments = append(segments, styledStatPair(formatStatsDuration(stats.ModelDuration), "model", styles))
	}
	if stats.Tools.Total > 0 {
		segments = append(segments, styledStatPair(formatStatsDuration(stats.Tools.Duration), "tools", styles))
	}
	if stats.ReviewDuration > 0 {
		segments = append(segments, styledStatPair(formatStatsDuration(stats.ReviewDuration), "review", styles))
	}
	if stats.FirstResponse > 0 {
		segments = append(segments, styledStatPair(formatStatsDuration(stats.FirstResponse), "first response", styles))
	}
	return styles.Comment.Render("  Time    ") + strings.Join(segments, styles.Comment.Render("  ·  "))
}

func hasRunStats(stats InteractionStats) bool {
	return stats.Provider != "" ||
		stats.Model != "" ||
		stats.ModelRounds > 0 ||
		stats.Tools.Total > 0 ||
		stats.Retries > 0 ||
		stats.Compactions > 0
}

func runStatsLine(stats InteractionStats) string {
	if !hasRunStats(stats) {
		return ""
	}
	var parts []string
	if modelLabel := formatModelLabel(stats.Provider, stats.Model); modelLabel != "" {
		parts = append(parts, modelLabel)
	}
	if stats.ModelRounds > 0 {
		parts = append(parts, formatCountWord(stats.ModelRounds, "round", "rounds"))
	}
	if stats.ModelRounds > 0 || stats.Tools.Total > 0 {
		toolPart := formatCountWord(stats.Tools.Total, "tool", "tools")
		if breakdown := toolOutcomeBreakdown(stats.Tools); len(breakdown) > 0 {
			toolPart += " (" + strings.Join(breakdown, ", ") + ")"
		}
		parts = append(parts, toolPart)
	}
	if stats.Retries > 0 {
		parts = append(parts, formatCountWord(stats.Retries, "retry", "retries"))
	}
	if stats.Compactions > 0 {
		parts = append(parts, formatCountWord(stats.Compactions, "compaction", "compactions"))
	}
	return "Run: " + strings.Join(parts, ", ")
}

func styledRunStatsLine(stats InteractionStats, styles Styles) string {
	if !hasRunStats(stats) {
		return ""
	}
	var segments []string
	if modelLabel := formatModelLabel(stats.Provider, stats.Model); modelLabel != "" {
		segments = append(segments, styles.Flag.Render(modelLabel))
	}
	if stats.ModelRounds > 0 {
		segments = append(segments, styledCountWord(stats.ModelRounds, "round", "rounds", styles))
	}
	if stats.ModelRounds > 0 || stats.Tools.Total > 0 {
		toolSeg := styledCountWord(stats.Tools.Total, "tool", "tools", styles)
		if breakdown := toolOutcomeBreakdown(stats.Tools); len(breakdown) > 0 {
			toolSeg += styles.Comment.Render(" (" + strings.Join(breakdown, ", ") + ")")
		}
		segments = append(segments, toolSeg)
	}
	if stats.Retries > 0 {
		segments = append(segments, styledCountWord(stats.Retries, "retry", "retries", styles))
	}
	if stats.Compactions > 0 {
		segments = append(segments, styledCountWord(stats.Compactions, "compaction", "compactions", styles))
	}
	return styles.Comment.Render("  Run     ") + strings.Join(segments, styles.Comment.Render("  ·  "))
}

func toolOutcomeBreakdown(tools ToolStats) []string {
	if tools.NonZeroExit == 0 && tools.Failed == 0 && tools.Denied == 0 && tools.Cancelled == 0 {
		return nil
	}
	var parts []string
	if tools.Succeeded > 0 {
		parts = append(parts, fmt.Sprintf("%d ok", tools.Succeeded))
	}
	if tools.NonZeroExit > 0 {
		parts = append(parts, fmt.Sprintf("%d non-zero", tools.NonZeroExit))
	}
	if tools.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", tools.Failed))
	}
	if tools.Denied > 0 {
		parts = append(parts, fmt.Sprintf("%d denied", tools.Denied))
	}
	if tools.Cancelled > 0 {
		parts = append(parts, fmt.Sprintf("%d cancelled", tools.Cancelled))
	}
	return parts
}

func formatModelLabel(provider, model string) string {
	switch {
	case provider != "" && model != "":
		return provider + "/" + model
	case model != "":
		return model
	default:
		return provider
	}
}

func styledStatPair(value, unit string, styles Styles) string {
	return styles.Flag.Render(value) + styles.Comment.Render(" "+unit)
}

func styledCountWord(n int, singular, plural string, styles Styles) string {
	unit := plural
	if n == 1 {
		unit = singular
	}
	return styledStatPair(formatTokenCount(int64(n)), unit, styles)
}

func formatCountWord(n int, singular, plural string) string {
	unit := plural
	if n == 1 {
		unit = singular
	}
	return formatTokenCount(int64(n)) + " " + unit
}

func outputTokensPerSecond(usage proto.TokenUsage, modelDuration time.Duration) string {
	if !usage.Available() || usage.OutputTokens <= 0 || modelDuration < 100*time.Millisecond {
		return ""
	}
	rate := float64(usage.OutputTokens) / modelDuration.Seconds()
	if rate >= 100 {
		return fmt.Sprintf("%.0f", rate)
	}
	return fmt.Sprintf("%.1f", rate)
}

func formatStatsDuration(d time.Duration) string {
	if d <= 0 {
		return "0ms"
	}
	if d < time.Millisecond {
		return "<1ms"
	}
	roundedMs := d.Round(time.Millisecond)
	if roundedMs < time.Second {
		return fmt.Sprintf("%dms", roundedMs/time.Millisecond)
	}
	if d < 10*time.Second {
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return d.Round(time.Second).String()
}

func formatTokenCount(value int64) string {
	digits := strconv.FormatInt(value, 10)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign = "-"
		digits = digits[1:]
	}
	if len(digits) <= 3 {
		return sign + digits
	}

	firstGroup := len(digits) % 3
	if firstGroup == 0 {
		firstGroup = 3
	}
	var result strings.Builder
	result.Grow(len(digits) + len(digits)/3)
	result.WriteString(sign)
	result.WriteString(digits[:firstGroup])
	for i := firstGroup; i < len(digits); i += 3 {
		result.WriteByte(',')
		result.WriteString(digits[i : i+3])
	}
	return result.String()
}
