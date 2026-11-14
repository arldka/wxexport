package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/arldka/wxexport/internal/wcdb"
)

func (m Model) updateConversation(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.screen = screenContacts
		return m, nil
	case "e":
		m.outDirInput.SetValue(".")
		m.exportResult, m.exportErr = nil, nil
		m.screen = screenExport
		return m, m.outDirInput.Focus()
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m Model) viewConversation() string {
	var b strings.Builder
	name := wcdb.SafeText(m.activeContact.DisplayName())
	fmt.Fprintf(&b, "%s  %s\n", titleStyle.Render(name), subtleStyle.Render(summaryLine(m.messages)))
	if m.convErr != nil {
		b.WriteString(errorStyle.Render("error: "+m.convErr.Error()) + "\n")
	} else {
		b.WriteString(m.vp.View() + "\n")
	}
	b.WriteString(helpStyle.Render("↑/↓ scroll   e export   esc back   q quit"))
	return b.String()
}

func summaryLine(msgs []wcdb.Message) string {
	n, calls, pics := len(msgs), 0, 0
	for _, m := range msgs {
		switch m.Decoded.Kind {
		case "call":
			calls++
		case "image":
			pics++
		}
	}
	return fmt.Sprintf("%d messages, %d calls, %d pictures", n, calls, pics)
}

// renderConversation builds the chat-bubble text shown in the viewport: a date separator
// whenever the day changes, and one line per message with a kind badge and sender hint.
func renderConversation(contact wcdb.ContactSummary, msgs []wcdb.Message) string {
	var b strings.Builder
	lastDate := ""
	for _, m := range msgs {
		date := time.Unix(m.TimestampUnix, 0).Format("2006-01-02")
		if date != lastDate {
			b.WriteString(dateSepStyle.Render("── "+date+" ──") + "\n")
			lastDate = date
		}
		ts := time.Unix(m.TimestampUnix, 0).Format("15:04")
		who := "?"
		style := subtleStyle
		switch m.Direction {
		case wcdb.DirSent:
			who, style = "me", sentStyle
		case wcdb.DirReceived:
			who, style = wcdb.SafeText(contact.DisplayName()), receivedStyle
		}
		b.WriteString(style.Render(fmt.Sprintf("[%s] %-12s %s", ts, who, wcdb.SafeText(badge(m)))) + "\n")
	}
	if len(msgs) == 0 {
		b.WriteString(subtleStyle.Render("(no messages)"))
	}
	return b.String()
}

func badge(m wcdb.Message) string {
	d := m.Decoded
	switch d.Kind {
	case "text":
		return d.Text
	case "image":
		return "[image]"
	case "sticker":
		return "[sticker]"
	case "voice":
		dur := 0.0
		if d.Media != nil {
			dur = float64(d.Media.DurationMS) / 1000
		}
		return fmt.Sprintf("[voice %.0fs]", dur)
	case "video":
		dur := int64(0)
		if d.Media != nil {
			dur = d.Media.DurationS
		}
		return fmt.Sprintf("[video %ds]", dur)
	case "call":
		if d.Call != nil {
			return fmt.Sprintf("[%s call · %s · %ds]", d.Call.Media, d.Call.Outcome, d.Call.DurationS)
		}
		return "[call]"
	case "recall":
		return "[message recalled]"
	case "system":
		return "[system] " + d.Text
	case "location":
		if d.Location != nil {
			return "[location] " + d.Location.Label
		}
		return "[location]"
	case "link":
		if d.Link != nil {
			return "[link] " + d.Link.Title
		}
		return "[link]"
	case "file":
		if d.File != nil {
			return "[file] " + d.File.Title
		}
		return "[file]"
	case "quote":
		return "[quote] " + d.Text
	default:
		return "[" + string(d.Kind) + "]"
	}
}
