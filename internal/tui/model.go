package tui

import (
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/arldka/wxexport/internal/wcdb"
)

// screen identifies which of the four screens is active.
type screen int

const (
	screenHome screen = iota
	screenContacts
	screenConversation
	screenExport
)

// --- async result messages ---

type statusLoadedMsg struct {
	status EnvStatus
	err    error
}
type contactsLoadedMsg struct {
	contacts []wcdb.ContactSummary
	err      error
}
type messagesLoadedMsg struct {
	contact  wcdb.ContactSummary
	messages []wcdb.Message
	err      error
}
type exportDoneMsg struct {
	summary ExportSummary
	err     error
}

func loadStatusCmd(b Backend) tea.Cmd {
	return func() tea.Msg {
		s, err := b.Status()
		return statusLoadedMsg{s, err}
	}
}

func loadContactsCmd(b Backend, showAll bool) tea.Cmd {
	return func() tea.Msg {
		cs, err := b.Contacts(showAll)
		return contactsLoadedMsg{cs, err}
	}
}

func loadMessagesCmd(b Backend, contact wcdb.ContactSummary) tea.Cmd {
	return func() tea.Msg {
		ms, err := b.Messages(contact.Username)
		return messagesLoadedMsg{contact, ms, err}
	}
}

func runExportCmd(b Backend, req ExportRequest) tea.Cmd {
	return func() tea.Msg {
		s, err := b.Export(req)
		return exportDoneMsg{s, err}
	}
}

// exportFocus enumerates the focusable fields on the export screen.
type exportFocus int

const (
	focusOutDir exportFocus = iota
	focusJSON
	focusCSV
	focusSQLite
	focusRaw
	focusFieldCount
)

// Model is the whole TUI's state. Fields are grouped by which screen they belong to; only the
// active screen's fields are meaningful at any time, but nothing is cleared on navigation (so
// going back and forward preserves state, e.g. the contact filter or export choices).
type Model struct {
	backend       Backend
	screen        screen
	width, height int
	quitting      bool

	// home
	status    EnvStatus
	statusErr error

	// contacts
	allContacts []wcdb.ContactSummary
	showAll     bool
	filtering   bool
	filterInput textinput.Model
	cursor      int
	contactsErr error

	// conversation
	activeContact wcdb.ContactSummary
	messages      []wcdb.Message
	vp            viewport.Model
	convErr       error

	// export
	outDirInput                         textinput.Model
	exportJSON, exportCSV, exportSQLite bool
	exportRaw                           bool
	focus                               exportFocus
	exportBusy                          bool
	exportResult                        *ExportSummary
	exportErr                           error
}

// New builds the initial model. The caller still must run it via tea.NewProgram.
func New(backend Backend) Model {
	fi := textinput.New()
	fi.Placeholder = "filter contacts..."
	od := textinput.New()
	od.Placeholder = "output directory"
	od.SetValue(".")
	vp := viewport.New()
	return Model{
		backend: backend, screen: screenHome,
		filterInput: fi, outDirInput: od, vp: vp,
		exportJSON: true, exportCSV: true, exportSQLite: true,
	}
}

func (m Model) Init() tea.Cmd { return loadStatusCmd(m.backend) }

func (m Model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	var body string
	switch m.screen {
	case screenHome:
		body = m.viewHome()
	case screenContacts:
		body = m.viewContacts()
	case screenConversation:
		body = m.viewConversation()
	case screenExport:
		body = m.viewExport()
	}
	return tea.NewView(body)
}
