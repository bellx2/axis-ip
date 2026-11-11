package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func typeText(m tuiModel, s string) tuiModel {
	for _, r := range s {
		mm, _ := m.Update(key(string(r)))
		m = mm.(tuiModel)
	}
	return m
}

func send(t *testing.T, m tuiModel, msg tea.Msg) tuiModel {
	t.Helper()
	mm, _ := m.Update(msg)
	return mm.(tuiModel)
}

// Walk list -> login -> detail -> edit -> confirm -> write against the fake camera.
func TestTUIFlow(t *testing.T) {
	t.Setenv(passwordEnv, "")
	cam := &fakeCamera{user: "root", pass: "secret"}
	srv := httptest.NewServer(cam)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	m := newTUIModel(time.Second, 5*time.Second)
	m = send(t, m, scanDoneMsg{devs: []Device{{Model: "P1455", MAC: "ACCC8E000001", IPv4: []string{host}}}})
	if !strings.Contains(m.View(), "AXIS IP Utility "+version) || !strings.Contains(m.View(), "by Bellx2") {
		t.Fatalf("title missing:\n%s", m.View())
	}
	if m.busy != "" || !strings.Contains(m.View(), "P1455") {
		t.Fatalf("device not listed:\n%s", m.View())
	}

	m = send(t, m, key("enter"))
	if m.screen != scrLogin {
		t.Fatalf("screen = %d", m.screen)
	}
	m = typeText(m, "secret")
	m = send(t, m, key("enter"))
	if m.busy == "" {
		t.Fatal("loading did not start")
	}
	m = send(t, m, m.getCmd()())
	if m.screen != scrDetail || !strings.Contains(m.View(), "192.168.0.20/24") {
		t.Fatalf("detail not shown: err=%v\n%s", m.err, m.View())
	}

	// Edit screen: switch to static and fill in the fields.
	m = send(t, m, key("e"))
	if m.screen != scrEdit || !m.dhcp || m.edit[fieldAddress].Value() != "192.168.0.20/24" {
		t.Fatalf("edit screen defaults: dhcp=%v addr=%q", m.dhcp, m.edit[fieldAddress].Value())
	}
	m = send(t, m, key(" ")) // DHCP -> static
	m = send(t, m, key("tab"))
	m.edit[fieldAddress].SetValue("192.168.0.90/24")
	m = send(t, m, key("tab"))
	m.edit[fieldGateway].SetValue("192.168.0.1")
	m = send(t, m, key("enter"))
	if m.screen != scrConfirm || !strings.Contains(m.View(), "set eth0 to static 192.168.0.90/24, gateway 192.168.0.1") {
		t.Fatalf("confirm screen: err=%v\n%s", m.err, m.View())
	}

	m = send(t, m, key("y"))
	m = send(t, m, m.setCmd()())
	if m.err != nil || m.screen != scrList || m.status == "" {
		t.Fatalf("after write: err=%v screen=%d", m.err, m.screen)
	}
	got, _ := json.Marshal(cam.lastSet)
	if !strings.Contains(string(got), `"address":"192.168.0.90"`) || !strings.Contains(string(got), `"deviceName":"eth0"`) {
		t.Fatalf("params = %s", got)
	}
}

// A wrong password returns to the login screen with an error.
func TestTUILoginFailure(t *testing.T) {
	t.Setenv(passwordEnv, "")
	srv := httptest.NewServer(&fakeCamera{user: "root", pass: "secret"})
	defer srv.Close()

	m := newTUIModel(time.Second, 5*time.Second)
	m = send(t, m, scanDoneMsg{devs: []Device{{Model: "P1455", IPv4: []string{strings.TrimPrefix(srv.URL, "http://")}}}})
	m = send(t, m, key("enter"))
	m = typeText(m, "wrong")
	m = send(t, m, key("enter"))
	m = send(t, m, m.getCmd()())
	if m.screen != scrLogin || m.err == nil || !strings.Contains(m.View(), "authentication failed") {
		t.Fatalf("screen=%d err=%v", m.screen, m.err)
	}
}

// Invalid input does not advance to the confirm screen.
func TestTUIEditValidation(t *testing.T) {
	m := newTUIModel(time.Second, time.Second)
	m.busy = "" // clear the initial scanning state
	m.screen = scrDetail
	m.net.Name = "eth0"
	m.net.IPv4.ConfigurationMode = "static"
	m = send(t, m, key("e"))
	m = send(t, m, key("tab"))
	m.edit[fieldAddress].SetValue("192.168.0.90")
	m = send(t, m, key("enter"))
	if m.screen != scrEdit || m.err == nil {
		t.Fatalf("screen=%d err=%v", m.screen, m.err)
	}
}

// The device table fits its rows instead of filling a tall terminal.
func TestTUITableFitsRows(t *testing.T) {
	m := newTUIModel(time.Second, time.Second)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 60})
	m = send(t, m, scanDoneMsg{devs: []Device{
		{Model: "P1455", MAC: "ACCC8E000001", IPv4: []string{"192.168.0.20"}},
		{Model: "M3086", MAC: "ACCC8E000002", IPv4: []string{"192.168.0.21"}},
	}})
	if lines := strings.Count(m.table.View(), "\n") + 1; lines > 5 {
		t.Fatalf("table uses %d lines for 2 rows:\n%s", lines, m.table.View())
	}

	// Rows beyond the terminal height scroll instead of overflowing.
	var many []Device
	for range 30 {
		many = append(many, Device{Model: "P1455", IPv4: []string{"192.168.0.20"}})
	}
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 20})
	m = send(t, m, scanDoneMsg{devs: many})
	if lines := strings.Count(m.table.View(), "\n") + 1; lines > 12 {
		t.Fatalf("table uses %d lines in a 20-line terminal", lines)
	}
}
