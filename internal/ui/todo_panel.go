package ui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type TodoItem struct {
	Content string
	Status  string
}

// RenderTodoDock bounds both dimensions. On short terminals a window of
// steps follows the active item, with counts indicating hidden steps.
func RenderTodoDock(styles InteractionStyles, width, height int, items []TodoItem) string {
	if len(items) == 0 || height <= styles.Panel.GetVerticalFrameSize()+1 {
		return ""
	}
	innerWidth := InteractionPanelInnerWidth(styles, width)
	rows := height - styles.Panel.GetVerticalFrameSize() - 1
	start, end := 0, len(items)
	if len(items) > rows {
		visible := max(1, rows-2)
		focus := 0
		for i, item := range items {
			if item.Status == "in_progress" {
				focus = i
				break
			}
			if item.Status == "completed" {
				focus = min(i+1, len(items)-1)
			}
		}
		start = max(0, min(focus-visible/2, len(items)-visible))
		end = min(len(items), start+visible)
	}
	body := make([]string, 0, rows)
	if start > 0 {
		body = append(body, styles.Muted.Render(fmt.Sprintf("↑ %d earlier steps", start)))
	}
	for i := start; i < end; i++ {
		item := items[i]
		item.Content = strings.Join(strings.Fields(item.Content), " ")
		body = append(body, ansi.Truncate(todoItemLine(styles, i+1, item), innerWidth, "…"))
	}
	if end < len(items) {
		body = append(body, styles.Muted.Render(fmt.Sprintf("↓ %d more steps", len(items)-end)))
	}
	if len(body) > rows {
		body = body[:rows]
	}
	completed, _ := todoCounts(items)
	return RenderInteractionPanel(styles, width, InteractionPanel{
		Title: "Plan", Meta: fmt.Sprintf("%d/%d completed", completed, len(items)),
		Tone: InteractionToneInfo, Body: body,
	})
}

func TodoItemsFromArgs(data []byte) []TodoItem {
	if len(data) == 0 {
		return nil
	}
	var args struct {
		Todos []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}
	if err := json.Unmarshal(data, &args); err != nil {
		return nil
	}
	items := make([]TodoItem, 0, len(args.Todos))
	for _, todo := range args.Todos {
		items = append(items, TodoItem{Content: strings.TrimSpace(todo.Content), Status: todo.Status})
	}
	if len(items) == 0 {
		return nil
	}
	return items
}

func TodoSummary(data []byte) string {
	items := TodoItemsFromArgs(data)
	if len(items) == 0 {
		return ""
	}
	completed, inProgress := todoCounts(items)
	var counts []string
	if completed > 0 {
		counts = append(counts, fmt.Sprintf("%d completed", completed))
	}
	if inProgress > 0 {
		counts = append(counts, fmt.Sprintf("%d in progress", inProgress))
	}
	if len(counts) == 0 {
		return fmt.Sprintf("%d items", len(items))
	}
	return fmt.Sprintf("%d items · %s", len(items), strings.Join(counts, ", "))
}

func todoCounts(items []TodoItem) (completed, inProgress int) {
	for _, item := range items {
		switch item.Status {
		case "completed":
			completed++
		case "in_progress":
			inProgress++
		}
	}
	return completed, inProgress
}

func RenderTodoPanel(styles InteractionStyles, width int, items []TodoItem) string {
	if len(items) == 0 {
		return ""
	}
	completed, inProgress := todoCounts(items)
	meta := fmt.Sprintf("%d/%d completed", completed, len(items))
	if inProgress > 0 {
		meta += fmt.Sprintf(" · %d in progress", inProgress)
	}
	body := make([]string, 0, len(items))
	for i, item := range items {
		body = append(body, todoItemLine(styles, i+1, item))
	}
	return RenderInteractionPanel(styles, width, InteractionPanel{
		Title: "Plan",
		Meta:  meta,
		Tone:  InteractionToneInfo,
		Body:  body,
	})
}

func todoMarker(status string) string {
	switch status {
	case "completed":
		return "[✓]"
	case "in_progress":
		return "[~]"
	default:
		return "[ ]"
	}
}

// TodoFooterLine renders the persistent one-line plan progress for the
// footer: "PLAN 1/3 · ▸ current step" while work is in progress, the first
// pending step when nothing is in progress, and "PLAN 3/3 done" once every
// step is completed.
func TodoFooterLine(styles Styles, items []TodoItem, width int) string {
	if len(items) == 0 {
		return ""
	}
	completed, _ := todoCounts(items)
	progress := fmt.Sprintf("%d/%d", completed, len(items))
	text := ""
	for i := range items {
		if items[i].Status == "in_progress" {
			text = progress + " · ▸ " + items[i].Content
			break
		}
	}
	if text == "" && completed < len(items) {
		for i := range items {
			if items[i].Status == "pending" {
				text = progress + " · " + items[i].Content
				break
			}
		}
	}
	if text == "" {
		text = progress + " done"
	}
	badge := styles.Interaction.Info.Render(fmt.Sprintf("%-4s", "PLAN"))
	textWidth := width - 5
	if textWidth <= 0 || textWidth > 120 {
		textWidth = 120
	}
	return badge + styles.Comment.Render(" "+TruncateOperationStatus(text, textWidth))
}

func TodoPlainText(items []TodoItem) string {
	if len(items) == 0 {
		return ""
	}
	completed, inProgress := todoCounts(items)
	var b strings.Builder
	fmt.Fprintf(&b, "Plan (%d items)", len(items))
	var counts []string
	if completed > 0 {
		counts = append(counts, fmt.Sprintf("%d completed", completed))
	}
	if inProgress > 0 {
		counts = append(counts, fmt.Sprintf("%d in progress", inProgress))
	}
	if len(counts) > 0 {
		b.WriteString(" — ")
		b.WriteString(strings.Join(counts, ", "))
	}
	for i, item := range items {
		fmt.Fprintf(&b, "\n%d. %s %s", i+1, todoMarker(item.Status), item.Content)
	}
	return b.String()
}

func todoItemLine(styles InteractionStyles, index int, item TodoItem) string {
	number := styles.Muted.Render(fmt.Sprintf("%d.", index))
	switch item.Status {
	case "completed":
		return number + " " + styles.Success.Render("[✓]") + " " + styles.Muted.Render(item.Content)
	case "in_progress":
		return number + " " + styles.Warning.Render("[~]") + " " + styles.Body.Render(item.Content)
	default:
		return number + " " + styles.Muted.Render("[ ]") + " " + styles.Body.Render(item.Content)
	}
}
