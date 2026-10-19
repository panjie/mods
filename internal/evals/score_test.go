package evals

import (
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func userMessage(text string) proto.Message {
	return proto.Message{Role: proto.RoleUser, Content: text}
}

func assistantText(text string) proto.Message {
	return proto.Message{Role: proto.RoleAssistant, Content: text}
}

func assistantTool(name, arguments string) proto.Message {
	return proto.Message{
		Role: proto.RoleAssistant,
		ToolCalls: []proto.ToolCall{{
			ID:       "call-" + name,
			Type:     "function",
			Function: proto.Function{Name: name, Arguments: []byte(arguments)},
		}},
	}
}

func toolOutput(text string) proto.Message {
	return proto.Message{Role: proto.RoleTool, Content: text}
}

func findDimension(t *testing.T, report Report, name string) Dimension {
	t.Helper()
	for _, dimension := range report.Dimensions {
		if dimension.Name == name {
			return dimension
		}
	}
	t.Fatalf("dimension %q missing from report", name)
	return Dimension{}
}

func TestScoreClarifyFirst(t *testing.T) {
	caseDef, ok := CaseByID(CaseAmbiguousSymptom)
	require.True(t, ok)

	t.Run("asking before probing passes", func(t *testing.T) {
		report := Score([]proto.Message{
			userMessage("我在插件里改的东西好像一直没体现"),
			assistantTool("request_user_input", `{"question":"哪个界面？"}`),
			assistantText("Observed: nothing loaded. Inferred: stale cache. Unverified: reload path."),
		}, caseDef)
		require.True(t, report.Passed(), "dimensions: %+v", report.Dimensions)
	})

	t.Run("text question before probing passes", func(t *testing.T) {
		report := Score([]proto.Message{
			userMessage("改了没生效"),
			assistantText("你指的是哪个界面、哪个操作？"),
		}, Case{RequiresClarification: true})
		require.True(t, report.Passed(), "dimensions: %+v", report.Dimensions)
	})

	t.Run("question followed by explanation passes", func(t *testing.T) {
		report := Score([]proto.Message{
			userMessage("Reduce one screen's font size."),
			assistantText("Which screen should I change? I have not inspected any files yet."),
		}, Case{RequiresClarification: true, ForbidsProbing: true})
		require.True(t, report.Passed(), "dimensions: %+v", report.Dimensions)
	})

	t.Run("probing first fails", func(t *testing.T) {
		report := Score([]proto.Message{
			userMessage("改了没生效"),
			assistantTool("fs_list_dir", `{"path":"."}`),
			assistantText("Observed: files. Inferred: cache. Unverified: reload."),
		}, caseDef)
		require.False(t, report.Passed())
		require.Equal(t, "fs_list_dir", report.FirstTool)
		require.False(t, findDimension(t, report, "clarify-first").Passed)
	})

	t.Run("clarification after probing does not count", func(t *testing.T) {
		report := Score([]proto.Message{
			userMessage("改了没生效"),
			assistantTool("fs_read_file", `{"path":"a"}`),
			assistantTool("request_user_input", `{"question":"哪个界面？"}`),
		}, Case{RequiresClarification: true})
		require.False(t, findDimension(t, report, "clarify-first").Passed)
	})
}

func TestScoreToolBudget(t *testing.T) {
	caseDef := Case{MaxToolCalls: 3}
	atLimit := []proto.Message{
		userMessage("go"),
		assistantTool("fs_read_file", `{"path":"a"}`),
		assistantTool("fs_read_file", `{"path":"b"}`),
		assistantTool("fs_read_file", `{"path":"c"}`),
	}
	report := Score(atLimit, caseDef)
	require.True(t, findDimension(t, report, "tool-budget").Passed)

	overLimit := append(atLimit, assistantTool("fs_read_file", `{"path":"d"}`))
	report = Score(overLimit, caseDef)
	require.False(t, findDimension(t, report, "tool-budget").Passed)
	require.Equal(t, 4, report.ToolCalls)
}

func TestScoreLiveTarget(t *testing.T) {
	caseDef := Case{TargetPath: "/work/plugin/Popup.qml", MaxToolCalls: 5}
	report := Score([]proto.Message{
		userMessage("改了没生效"),
		assistantTool("fs_read_file", `{"path":"/work/plugin/Popup.qml"}`),
	}, caseDef)
	require.True(t, findDimension(t, report, "live-target").Passed)

	report = Score([]proto.Message{
		userMessage("改了没生效"),
		assistantTool("fs_read_file", `{"path":"/tmp/copy/Popup.qml"}`),
	}, caseDef)
	require.False(t, findDimension(t, report, "live-target").Passed)
}

func TestScoreExpectedValue(t *testing.T) {
	caseDef := Case{ExpectedProbeToken: "#cacccc", MaxToolCalls: 5}
	report := Score([]proto.Message{
		userMessage("颜色对不上"),
		assistantTool("shell_run", `{"command":"probe"}`),
		toolOutput("foreground=#cacccc"),
	}, caseDef)
	require.True(t, findDimension(t, report, "expected-value").Passed)

	report = Score([]proto.Message{
		userMessage("颜色对不上"),
		assistantTool("shell_run", `{"command":"probe"}`),
		toolOutput("no warnings"),
	}, caseDef)
	require.False(t, findDimension(t, report, "expected-value").Passed)
}

func TestScoreEvidenceLabels(t *testing.T) {
	caseDef := Case{RequiresEvidenceLabels: true}

	t.Run("english labels pass", func(t *testing.T) {
		report := Score([]proto.Message{
			assistantText("Observed: reload fired. Inferred: stale cache. Unverified: visual result."),
		}, caseDef)
		require.True(t, findDimension(t, report, "evidence-labels").Passed)
	})

	t.Run("chinese labels pass", func(t *testing.T) {
		report := Score([]proto.Message{
			assistantText("观察到日志有重载。推断是缓存。未验证界面效果。"),
		}, caseDef)
		require.True(t, findDimension(t, report, "evidence-labels").Passed)
	})

	t.Run("one group fails", func(t *testing.T) {
		report := Score([]proto.Message{
			assistantText("这只是推断，颜色可能是 undefined。"),
		}, caseDef)
		require.False(t, findDimension(t, report, "evidence-labels").Passed)
	})

	t.Run("no final text fails", func(t *testing.T) {
		report := Score([]proto.Message{
			userMessage("hi"),
			assistantTool("fs_read_file", `{"path":"a"}`),
		}, caseDef)
		require.False(t, findDimension(t, report, "evidence-labels").Passed)
	})
}

func TestScoreNotesTemporaryWrites(t *testing.T) {
	caseDef := Case{TargetPath: "/work/plugin/Settings.qml", MaxToolCalls: 5}
	report := Score([]proto.Message{
		userMessage("改了没生效"),
		assistantTool("fs_write_file", `{"path":"/tmp/probe.qml"}`),
		assistantTool("fs_read_file", `{"path":"/work/plugin/Settings.qml"}`),
		assistantText("Observed: x. Inferred: y. Unverified: z. See /tmp/probe.qml."),
	}, caseDef)
	require.Contains(t, report.Notes, "1 write tool call(s) targeted a temporary path")
	require.Contains(t, report.Notes, "a temporary write preceded the first live-target inspection")
	require.Contains(t, report.Notes, "the final answer cites a temporary path")
}

func TestCasesAreValid(t *testing.T) {
	seen := make(map[string]bool)
	for _, c := range Cases() {
		require.NotEmpty(t, c.ID)
		require.NotEmpty(t, c.Description)
		require.False(t, seen[c.ID], "duplicate case id %q", c.ID)
		seen[c.ID] = true
		require.GreaterOrEqual(t, c.Budget(), 1)
	}
	_, ok := CaseByID("missing")
	require.False(t, ok)
}

func TestReportCounts(t *testing.T) {
	report := Report{Dimensions: []Dimension{
		{Name: "a", Passed: true},
		{Name: "b", Passed: false},
		{Name: "c", Passed: true},
	}}
	require.Equal(t, 2, report.PassedCount())
	require.Equal(t, 3, report.Total())
	require.False(t, report.Passed())

	report.Dimensions[1].Passed = true
	require.True(t, report.Passed())
}
