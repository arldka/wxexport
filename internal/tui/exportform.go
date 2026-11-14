package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m Model) updateExport(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.outDirInput.Blur()
		m.screen = screenConversation
		return m, nil
	case "tab", "shift+tab":
		if msg.String() == "tab" {
			m.focus = (m.focus + 1) % focusFieldCount
		} else {
			m.focus = (m.focus - 1 + focusFieldCount) % focusFieldCount
		}
		if m.focus == focusOutDir {
			return m, m.outDirInput.Focus()
		}
		m.outDirInput.Blur()
		return m, nil
	case "enter":
		if m.exportBusy {
			return m, nil
		}
		var formats []string
		if m.exportJSON {
			formats = append(formats, "json")
		}
		if m.exportCSV {
			formats = append(formats, "csv")
		}
		if m.exportSQLite {
			formats = append(formats, "sqlite")
		}
		if len(formats) == 0 {
			m.exportErr = fmt.Errorf("select at least one format")
			return m, nil
		}
		m.exportBusy, m.exportResult, m.exportErr = true, nil, nil
		req := ExportRequest{Username: m.activeContact.Username, Formats: formats, OutDir: m.outDirInput.Value(), Raw: m.exportRaw}
		return m, runExportCmd(m.backend, req)
	case "space":
		switch m.focus {
		case focusJSON:
			m.exportJSON = !m.exportJSON
		case focusCSV:
			m.exportCSV = !m.exportCSV
		case focusSQLite:
			m.exportSQLite = !m.exportSQLite
		case focusRaw:
			m.exportRaw = !m.exportRaw
		}
		return m, nil
	}
	if m.focus == focusOutDir {
		var cmd tea.Cmd
		m.outDirInput, cmd = m.outDirInput.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) viewExport() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n\n", titleStyle.Render("Export"), subtleStyle.Render(m.activeContact.DisplayName()))
	fmt.Fprintf(&b, "%s Output dir: %s\n", focusMark(m.focus == focusOutDir), m.outDirInput.View())
	fmt.Fprintf(&b, "%s %s JSON\n", focusMark(m.focus == focusJSON), checkbox(m.exportJSON))
	fmt.Fprintf(&b, "%s %s CSV\n", focusMark(m.focus == focusCSV), checkbox(m.exportCSV))
	fmt.Fprintf(&b, "%s %s SQLite\n", focusMark(m.focus == focusSQLite), checkbox(m.exportSQLite))
	fmt.Fprintf(&b, "%s %s include raw content\n", focusMark(m.focus == focusRaw), checkbox(m.exportRaw))
	b.WriteString("\n")

	switch {
	case m.exportBusy:
		b.WriteString(subtleStyle.Render("exporting...") + "\n")
	case m.exportErr != nil:
		b.WriteString(errorStyle.Render("error: "+m.exportErr.Error()) + "\n")
	case m.exportResult != nil:
		b.WriteString(okStyle.Render(fmt.Sprintf("exported %d message(s):", m.exportResult.Count)) + "\n")
		for _, f := range m.exportResult.Files {
			b.WriteString("  " + f + "\n")
		}
	}
	b.WriteString("\n" + helpStyle.Render("tab/shift+tab move   space toggle   enter run   esc back"))
	return b.String()
}

func focusMark(on bool) string {
	if on {
		return selectedStyle.Render(">")
	}
	return " "
}

func checkbox(on bool) string {
	if on {
		return "[x]"
	}
	return "[ ]"
}
