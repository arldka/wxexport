package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/arldka/wxexport/internal/msg"
	"github.com/arldka/wxexport/internal/wcdb"
)

// fakeBackend is a scriptable Backend for model tests: no real store, no real filesystem I/O.
type fakeBackend struct {
	status        EnvStatus
	statusErr     error
	contacts      []wcdb.ContactSummary
	contactsErr   error
	messagesByUse map[string][]wcdb.Message
	messagesErr   error
	exportResult  ExportSummary
	exportErr     error
	lastExportReq ExportRequest
}

func (f *fakeBackend) Status() (EnvStatus, error) { return f.status, f.statusErr }
func (f *fakeBackend) Contacts(showAll bool) ([]wcdb.ContactSummary, error) {
	return f.contacts, f.contactsErr
}
func (f *fakeBackend) Messages(username string) ([]wcdb.Message, error) {
	if f.messagesErr != nil {
		return nil, f.messagesErr
	}
	return f.messagesByUse[username], nil
}
func (f *fakeBackend) Export(req ExportRequest) (ExportSummary, error) {
	f.lastExportReq = req
	return f.exportResult, f.exportErr
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case " ":
		return tea.KeyPressMsg{Code: ' ', Text: " "}
	default:
		if len(s) == 1 {
			return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
		}
		return tea.KeyPressMsg{Code: rune(s[0])}
	}
}

func newTestModel(b Backend) Model {
	m := New(b)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m2.(Model)
}

func TestInitLoadsStatus(t *testing.T) {
	fb := &fakeBackend{status: EnvStatus{Account: "wxid_me", KeyPresent: true}}
	m := newTestModel(fb)
	cmd := m.Init()
	msg := cmd()
	m2, _ := m.Update(msg)
	m = m2.(Model)
	if m.status.Account != "wxid_me" || !m.status.KeyPresent {
		t.Fatalf("status = %+v", m.status)
	}
	if !strings.Contains(m.View().Content, "wxid_me") {
		t.Errorf("view does not show account:\n%s", m.View().Content)
	}
}

func TestHomeToContactsRequiresKey(t *testing.T) {
	fb := &fakeBackend{status: EnvStatus{KeyPresent: false}}
	m := newTestModel(fb)
	m.status = fb.status
	m2, cmd := m.Update(key("enter"))
	m = m2.(Model)
	if cmd != nil {
		t.Error("enter without a key must not start loading contacts")
	}
	if m.screen != screenHome {
		t.Errorf("screen = %v, want home", m.screen)
	}
}

func contactsFixture() []wcdb.ContactSummary {
	return []wcdb.ContactSummary{
		{Contact: wcdb.Contact{Username: "wxid_alice", Remark: "Alice"}, LastTimestamp: 200},
		{Contact: wcdb.Contact{Username: "wxid_bob", Remark: "Bob"}, LastTimestamp: 100},
	}
}

func modelAtContacts(fb *fakeBackend) Model {
	m := newTestModel(fb)
	m.status = fb.status
	m2, _ := m.Update(key("enter")) // -> loadContactsCmd
	m = m2.(Model)
	mc, _ := m.Update(contactsLoadedMsg{contacts: fb.contacts, err: fb.contactsErr})
	return mc.(Model)
}

func TestContactsListAndNavigate(t *testing.T) {
	fb := &fakeBackend{status: EnvStatus{KeyPresent: true}, contacts: contactsFixture()}
	m := modelAtContacts(fb)
	if m.screen != screenContacts {
		t.Fatalf("screen = %v", m.screen)
	}
	if !strings.Contains(m.viewContacts(), "Alice") || !strings.Contains(m.viewContacts(), "Bob") {
		t.Errorf("view missing contacts:\n%s", m.viewContacts())
	}
	if m.cursor != 0 {
		t.Fatalf("initial cursor = %d", m.cursor)
	}
	m2, _ := m.Update(key("down"))
	m = m2.(Model)
	if m.cursor != 1 {
		t.Errorf("cursor after down = %d", m.cursor)
	}
	m2, _ = m.Update(key("down")) // already at the end, must not go out of range
	m = m2.(Model)
	if m.cursor != 1 {
		t.Errorf("cursor overran = %d", m.cursor)
	}
}

func TestContactsFilter(t *testing.T) {
	fb := &fakeBackend{status: EnvStatus{KeyPresent: true}, contacts: contactsFixture()}
	m := modelAtContacts(fb)
	m2, _ := m.Update(key("/"))
	m = m2.(Model)
	if !m.filtering {
		t.Fatal("expected filtering mode")
	}
	for _, r := range "bob" {
		m2, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = m2.(Model)
	}
	list := m.visibleContacts()
	if len(list) != 1 || list[0].Username != "wxid_bob" {
		t.Fatalf("filtered list = %+v", list)
	}
	m2, _ = m.Update(key("enter")) // confirm filter
	m = m2.(Model)
	if m.filtering {
		t.Error("still filtering after enter")
	}
}

func TestContactsEnterLoadsMessages(t *testing.T) {
	fb := &fakeBackend{status: EnvStatus{KeyPresent: true}, contacts: contactsFixture(),
		messagesByUse: map[string][]wcdb.Message{"wxid_alice": {
			{LocalID: 1, TimestampUnix: 1700000000, Direction: wcdb.DirReceived, Decoded: msg.Decode(1, "hi", "")},
		}}}
	m := modelAtContacts(fb)
	m2, cmd := m.Update(key("enter"))
	m = m2.(Model)
	if cmd == nil {
		t.Fatal("expected a load-messages command")
	}
	m2, _ = m.Update(cmd())
	m = m2.(Model)
	if m.screen != screenConversation {
		t.Fatalf("screen = %v", m.screen)
	}
	if len(m.messages) != 1 {
		t.Fatalf("messages = %+v", m.messages)
	}
	if !strings.Contains(m.viewConversation(), "hi") {
		t.Errorf("conversation view missing text:\n%s", m.viewConversation())
	}
}

func TestContactsErrorShown(t *testing.T) {
	fb := &fakeBackend{status: EnvStatus{KeyPresent: true}, contactsErr: errors.New("boom")}
	m := modelAtContacts(fb)
	if !strings.Contains(m.viewContacts(), "boom") {
		t.Errorf("view does not show error:\n%s", m.viewContacts())
	}
}

func modelAtConversation(fb *fakeBackend, contact wcdb.ContactSummary, msgs []wcdb.Message) Model {
	m := modelAtContacts(fb)
	m2, _ := m.Update(messagesLoadedMsg{contact: contact, messages: msgs})
	return m2.(Model)
}

func TestConversationExportKeyGoesToExportScreen(t *testing.T) {
	fb := &fakeBackend{status: EnvStatus{KeyPresent: true}, contacts: contactsFixture()}
	m := modelAtConversation(fb, contactsFixture()[0], nil)
	m2, _ := m.Update(key("e"))
	m = m2.(Model)
	if m.screen != screenExport {
		t.Fatalf("screen = %v", m.screen)
	}
}

func TestConversationEscGoesBackToContacts(t *testing.T) {
	fb := &fakeBackend{status: EnvStatus{KeyPresent: true}, contacts: contactsFixture()}
	m := modelAtConversation(fb, contactsFixture()[0], nil)
	m2, _ := m.Update(key("esc"))
	m = m2.(Model)
	if m.screen != screenContacts {
		t.Fatalf("screen = %v", m.screen)
	}
}

func TestExportFormToggleAndRun(t *testing.T) {
	fb := &fakeBackend{status: EnvStatus{KeyPresent: true}, contacts: contactsFixture(),
		exportResult: ExportSummary{Count: 3, Files: []string{"a.json", "a_messages.csv"}}}
	m := modelAtConversation(fb, contactsFixture()[0], nil)
	m2, _ := m.Update(key("e"))
	m = m2.(Model)

	// Defaults: all three formats on.
	if !m.exportJSON || !m.exportCSV || !m.exportSQLite {
		t.Fatalf("defaults = json=%v csv=%v sqlite=%v", m.exportJSON, m.exportCSV, m.exportSQLite)
	}
	// Move focus to the JSON checkbox and toggle it off.
	m2, _ = m.Update(key("tab"))
	m = m2.(Model)
	m2, _ = m.Update(key(" "))
	m = m2.(Model)
	if m.exportJSON {
		t.Fatal("expected JSON toggled off")
	}

	m2, cmd := m.Update(key("enter"))
	m = m2.(Model)
	if !m.exportBusy {
		t.Fatal("expected exportBusy=true right after enter")
	}
	if cmd == nil {
		t.Fatal("expected a run-export command")
	}
	m2, _ = m.Update(cmd())
	m = m2.(Model)
	if m.exportBusy {
		t.Error("exportBusy should clear once the result arrives")
	}
	if m.exportResult == nil || m.exportResult.Count != 3 {
		t.Fatalf("exportResult = %+v", m.exportResult)
	}
	if !strings.Contains(m.viewExport(), "a.json") {
		t.Errorf("export view missing file list:\n%s", m.viewExport())
	}
	if fb.lastExportReq.Formats[0] != "csv" { // json was toggled off, so csv should be first
		t.Errorf("export request formats = %v", fb.lastExportReq.Formats)
	}
}

func TestExportFormRejectsNoFormats(t *testing.T) {
	fb := &fakeBackend{status: EnvStatus{KeyPresent: true}, contacts: contactsFixture()}
	m := modelAtConversation(fb, contactsFixture()[0], nil)
	m2, _ := m.Update(key("e"))
	m = m2.(Model)
	for _, f := range []exportFocus{focusJSON, focusCSV, focusSQLite} {
		m.focus = f
		m2, _ = m.Update(key(" "))
		m = m2.(Model)
	}
	m2, cmd := m.Update(key("enter"))
	m = m2.(Model)
	if cmd != nil {
		t.Error("expected no command when no format is selected")
	}
	if m.exportErr == nil {
		t.Error("expected a validation error")
	}
}

func TestExportEscGoesBackToConversation(t *testing.T) {
	fb := &fakeBackend{status: EnvStatus{KeyPresent: true}, contacts: contactsFixture()}
	m := modelAtConversation(fb, contactsFixture()[0], nil)
	m2, _ := m.Update(key("e"))
	m = m2.(Model)
	m2, _ = m.Update(key("esc"))
	m = m2.(Model)
	if m.screen != screenConversation {
		t.Fatalf("screen = %v", m.screen)
	}
}

func TestQuitFromEveryScreen(t *testing.T) {
	fb := &fakeBackend{status: EnvStatus{KeyPresent: true}, contacts: contactsFixture()}
	screens := []func() Model{
		func() Model { return newTestModel(fb) },
		func() Model { return modelAtContacts(fb) },
		func() Model { return modelAtConversation(fb, contactsFixture()[0], nil) },
	}
	for _, mk := range screens {
		m := mk()
		m2, cmd := m.Update(key("q"))
		m = m2.(Model)
		if !m.quitting || cmd == nil {
			t.Errorf("q did not quit from screen %v", m.screen)
		}
	}
}

func TestWindowResizeAdjustsViewport(t *testing.T) {
	m := New(&fakeBackend{})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = m2.(Model)
	if m.vp.Width() != 100 {
		t.Errorf("viewport width = %d", m.vp.Width())
	}
}

func TestRenderConversationDateSeparatorAndKinds(t *testing.T) {
	contact := wcdb.ContactSummary{Contact: wcdb.Contact{Username: "wxid_alice", Remark: "Alice"}}
	day1 := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC).Unix()
	day2 := time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC).Unix()
	msgs := []wcdb.Message{
		{TimestampUnix: day1, Direction: wcdb.DirReceived, Decoded: msg.Decode(1, "hello", "")},
		{TimestampUnix: day2, Direction: wcdb.DirSent, Decoded: msg.Decode(50,
			`<voipmsg type="VoIPBubbleMsg"><VoIPBubbleMsg><msg><![CDATA[Duration: 01:30]]></msg><room_type>0</room_type></VoIPBubbleMsg></voipmsg>`, "")},
	}
	out := renderConversation(contact, msgs)
	if !strings.Contains(out, "2024-01-01") || !strings.Contains(out, "2024-01-02") {
		t.Errorf("missing date separators:\n%s", out)
	}
	if !strings.Contains(out, "voice call") && !strings.Contains(out, "call") {
		t.Errorf("missing call badge:\n%s", out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("missing text content:\n%s", out)
	}
}

func TestRenderConversationEmpty(t *testing.T) {
	out := renderConversation(wcdb.ContactSummary{}, nil)
	if !strings.Contains(out, "no messages") {
		t.Errorf("empty conversation should say so: %q", out)
	}
}
