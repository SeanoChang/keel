package comms

import (
	"fmt"
	"strings"
	"time"
)

// Scope controls which rows render and how they're laid out.
type Scope struct {
	Title       string
	AgentFilter string // "" = global; otherwise only rows touching this agent
}

const (
	// maxBody leaves headroom under Discord's 2000-char message body limit so
	// truncation footer + code-block fences fit comfortably.
	maxBody         = 1900
	subjectMaxWidth = 20
)

// Render returns a complete Discord message body for the given snapshot under
// the given scope. Output is a fenced code block for monospace alignment.
func Render(events []Event, scope Scope, now time.Time) string {
	filtered := events
	if scope.AgentFilter != "" {
		filtered = make([]Event, 0, len(events))
		for _, ev := range events {
			if ev.From == scope.AgentFilter || ev.To == scope.AgentFilter {
				filtered = append(filtered, ev)
			}
		}
	}

	header := fmt.Sprintf("%s — last 24h — updated %s UTC", scope.Title, now.UTC().Format("15:04"))

	if len(filtered) == 0 {
		return fmt.Sprintf("```\n%s\n\n(no traffic in the last 24h)\n```", header)
	}

	if scope.AgentFilter == "" {
		return renderGlobal(filtered, header)
	}
	return renderPerAgent(filtered, scope.AgentFilter, header)
}

func renderGlobal(events []Event, header string) string {
	const (
		fromW = 8
		toW   = 8
	)
	const tplRow = " %-5s  %-*s %-*s %-*s %-9s"
	headerRow := fmt.Sprintf(tplRow, "time", fromW, "from", toW, "to", subjectMaxWidth, "subject", "status")
	rule := " " + strings.Repeat("─", 5) + "  " +
		strings.Repeat("─", fromW) + " " +
		strings.Repeat("─", toW) + " " +
		strings.Repeat("─", subjectMaxWidth) + " " +
		strings.Repeat("─", 9)

	rows := make([]string, 0, len(events))
	for _, ev := range events {
		rows = append(rows, fmt.Sprintf(tplRow,
			ev.Timestamp.UTC().Format("15:04"),
			fromW, trunc(ev.From, fromW),
			toW, trunc(ev.To, toW),
			subjectMaxWidth, formatSubject(ev),
			renderStatus(ev),
		))
	}

	return assembleCodeBlock(header, headerRow, rule, rows)
}

func renderPerAgent(events []Event, agent, header string) string {
	const otherW = 8
	const tplRow = " %-5s  %s   %-*s %-*s %-9s"
	headerRow := fmt.Sprintf(" %-5s  %-3s %-*s %-*s %-9s",
		"time", "dir", otherW, "other", subjectMaxWidth, "subject", "status")
	rule := " " + strings.Repeat("─", 5) + "  " +
		strings.Repeat("─", 3) + " " +
		strings.Repeat("─", otherW) + " " +
		strings.Repeat("─", subjectMaxWidth) + " " +
		strings.Repeat("─", 9)

	rows := make([]string, 0, len(events))
	for _, ev := range events {
		dir := "→"
		other := ev.To
		if ev.To == agent {
			dir = "←"
			other = ev.From
		}
		rows = append(rows, fmt.Sprintf(tplRow,
			ev.Timestamp.UTC().Format("15:04"),
			dir,
			otherW, trunc(other, otherW),
			subjectMaxWidth, formatSubject(ev),
			renderStatus(ev),
		))
	}

	return assembleCodeBlock(header, headerRow, rule, rows)
}

func assembleCodeBlock(header, columnHeader, rule string, rows []string) string {
	// Build body, dropping oldest rows if we exceed the size cap.
	prefix := "```\n" + header + "\n\n" + columnHeader + "\n" + rule + "\n"
	suffix := "```"
	omitted := 0

	for {
		body := strings.Join(rows, "\n")
		final := prefix + body + "\n" + suffix
		if omitted > 0 {
			footer := fmt.Sprintf("\n… %d earlier omitted", omitted)
			final = prefix + body + footer + "\n" + suffix
		}
		if len(final) <= maxBody || len(rows) == 0 {
			return final
		}
		// Drop the oldest (slice is sorted ascending).
		rows = rows[1:]
		omitted++
	}
}

func formatSubject(ev Event) string {
	subj := ev.Subject
	if ev.Kind == KindDelegation {
		// Reserve room for the suffix (" [deleg]" = 8 chars, optionally plus
		// " N/M" progress) by truncating the raw subject first; otherwise a
		// long subject would push the suffix off the right edge.
		suffix := " [deleg]"
		if ev.SubTasksTotal > 0 && ev.SubTasksDone < ev.SubTasksTotal {
			suffix = fmt.Sprintf(" %d/%d [deleg]", ev.SubTasksDone, ev.SubTasksTotal)
		}
		room := subjectMaxWidth - len(suffix)
		if room < 1 {
			room = 1
		}
		subj = trunc(subj, room) + suffix
	}
	return trunc(subj, subjectMaxWidth)
}

func renderStatus(ev Event) string {
	return string(ev.Status)
}

func trunc(s string, n int) string {
	if n <= 1 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
