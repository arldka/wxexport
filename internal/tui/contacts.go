package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/arldka/wxexport/internal/wcdb"
)

// visibleContacts applies the current filter text (case-insensitive substring over display
// name and username) to allContacts.
func (m Model) visibleContacts() []wcdb.ContactSummary {
	q := strings.ToLower(strings.TrimSpace(m.filterInput.Value()))
	if q == "" {
		return m.allContacts
	}
	var out []wcdb.ContactSummary
	for _, c := range m.allContacts {
		if strings.Contains(strings.ToLower(c.DisplayName()), q) || strings.Contains(strings.ToLower(c.Username), q) {
			out = append(out, c)
		}
	}
	return out
}

func (m Model) updateContacts(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.filtering {
		switch msg.String() {
		case "esc":
			m.filtering = false
			m.filterInput.SetValue("")
			m.filterInput.Blur()
			m.cursor = 0
			return m, nil
		case "enter":
			m.filtering = false
			m.filterInput.Blur()
			m.cursor = 0
			return m, nil
		}
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.cursor = 0
		return m, cmd
	}

	list := m.visibleContacts()
	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.screen = screenHome
		return m, nil
	case "/":
		m.filtering = true
		return m, m.filterInput.Focus()
	case "a":
		m.showAll = !m.showAll
		return m, loadContactsCmd(m.backend, m.showAll)
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "down", "j":
		if m.cursor < len(list)-1 {
			m.cursor++
		}
		return m, nil
	case "enter":
		if m.cursor >= 0 && m.cursor < len(list) {
			return m, loadMessagesCmd(m.backend, list[m.cursor])
		}
	}
	return m, nil
}

func (m Model) viewContacts() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Contacts") + "\n")
	if m.filtering {
		b.WriteString("/" + m.filterInput.View() + "\n")
	} else if v := m.filterInput.Value(); v != "" {
		fmt.Fprintf(&b, "filter: %q (esc on filter to clear)\n", v)
	}
	if m.contactsErr != nil {
		b.WriteString(errorStyle.Render("error: "+m.contactsErr.Error()) + "\n")
		return b.String()
	}
	list := m.visibleContacts()
	for i, c := range list {
		line := fmt.Sprintf("%-25s %-22s %s", wcdb.SafeText(c.DisplayName()), wcdb.SafeText(c.Username), lastActivity(c))
		if i == m.cursor && !m.filtering {
			line = selectedStyle.Render("> " + line)
		} else {
			line = "  " + line
		}
		b.WriteString(line + "\n")
	}
	fmt.Fprintf(&b, "\n%d contact(s)%s\n", len(list), map[bool]string{true: " (showing all)", false: ""}[m.showAll])
	b.WriteString(helpStyle.Render("/ filter   a toggle groups/official   enter open   esc back   q quit"))
	return b.String()
}

func lastActivity(c wcdb.ContactSummary) string {
	if c.LastTimestamp == 0 {
		return "-"
	}
	return time.Unix(c.LastTimestamp, 0).Format("2006-01-02")
}
