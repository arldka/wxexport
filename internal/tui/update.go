package tui

import (
	tea "charm.land/bubbletea/v2"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.vp.SetWidth(m.width)
		m.vp.SetHeight(max(m.height-4, 1))
		return m, nil

	case statusLoadedMsg:
		m.status, m.statusErr = msg.status, msg.err
		return m, nil

	case contactsLoadedMsg:
		m.allContacts, m.contactsErr = msg.contacts, msg.err
		m.screen = screenContacts
		if m.cursor >= len(m.visibleContacts()) {
			m.cursor = 0
		}
		return m, nil

	case messagesLoadedMsg:
		m.activeContact, m.messages, m.convErr = msg.contact, msg.messages, msg.err
		m.vp.SetContent(renderConversation(msg.contact, msg.messages))
		m.vp.GotoBottom()
		m.screen = screenConversation
		return m, nil

	case exportDoneMsg:
		m.exportBusy = false
		if msg.err != nil {
			m.exportErr, m.exportResult = msg.err, nil
		} else {
			m.exportErr, m.exportResult = nil, &msg.summary
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch m.screen {
	case screenHome:
		return m.updateHome(msg)
	case screenContacts:
		return m.updateContacts(msg)
	case screenConversation:
		return m.updateConversation(msg)
	case screenExport:
		return m.updateExport(msg)
	}
	return m, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
