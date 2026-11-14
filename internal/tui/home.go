package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m Model) updateHome(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "r":
		return m, loadStatusCmd(m.backend)
	case "enter":
		if m.statusErr == nil && m.status.KeyPresent {
			return m, loadContactsCmd(m.backend, m.showAll)
		}
	}
	return m, nil
}

func (m Model) viewHome() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("wxexport") + "\n\n")
	if m.statusErr != nil {
		b.WriteString(errorStyle.Render("error: "+m.statusErr.Error()) + "\n")
	} else {
		fmt.Fprintf(&b, "WeChat version : %s\n", orDash(m.status.InstallVersion))
		fmt.Fprintf(&b, "account        : %s\n", orDash(m.status.Account))
		fmt.Fprintf(&b, "running        : %v\n", m.status.Running)
		keyLine := "no — run `wxexport key capture` in a terminal first"
		if m.status.KeyPresent {
			keyLine = fmt.Sprintf("yes (%d/%d databases keyed)", m.status.DBKeyed, m.status.DBTotal)
		}
		fmt.Fprintf(&b, "key captured   : %s\n", keyLine)
		if !m.status.SnapshotAt.IsZero() {
			fmt.Fprintf(&b, "last snapshot  : %s\n", m.status.SnapshotAt.Format("2006-01-02 15:04:05"))
		}
	}
	b.WriteString("\n")
	if m.status.KeyPresent {
		b.WriteString(okStyle.Render("enter") + " browse contacts   ")
	}
	b.WriteString(helpStyle.Render("r refresh   q quit"))
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
