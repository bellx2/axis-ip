package main

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBrowserCommand(t *testing.T) {
	const url = "http://192.168.0.20/"
	for goos, want := range map[string][]string{
		"darwin":  {"open", url},
		"linux":   {"xdg-open", url},
		"freebsd": {"xdg-open", url},
		"windows": {"rundll32", "url.dll,FileProtocolHandler", url},
	} {
		name, args := browserCommand(goos, url)
		if got := append([]string{name}, args...); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", goos, got, want)
		}
	}
}

func TestDeviceURL(t *testing.T) {
	for _, c := range []struct {
		ip     string
		port   int
		scheme string
		want   string
	}{
		{"192.168.0.20", 80, "", "http://192.168.0.20/"},
		{"192.168.0.20", 0, "http", "http://192.168.0.20/"},
		{"192.168.0.20", 8080, "", "http://192.168.0.20:8080/"},
		{"192.168.0.20", 80, "https", "https://192.168.0.20/"},
	} {
		if got := deviceURL(c.ip, c.port, c.scheme); got != c.want {
			t.Errorf("deviceURL(%q, %d, %q) = %q, want %q", c.ip, c.port, c.scheme, got, c.want)
		}
	}
}

// stubOpenURL records the URLs the TUI asks to open.
func stubOpenURL(t *testing.T, err error) *[]string {
	t.Helper()
	var opened []string
	orig := openURL
	openURL = func(url string) error { opened = append(opened, url); return err }
	t.Cleanup(func() { openURL = orig })
	return &opened
}

func TestTUIOpenBrowserFromList(t *testing.T) {
	opened := stubOpenURL(t, nil)
	m := newTUIModel(time.Second, time.Second)
	m = send(t, m, scanDoneMsg{devs: []Device{
		{Model: "P1455", MAC: "ACCC8E000001", Port: 80, IPv4: []string{"192.168.0.20", "169.254.10.20"}},
		{Model: "M3086", MAC: "ACCC8E000002", Port: 8080, IPv4: []string{"192.168.0.21"}},
	}})

	mm, cmd := m.Update(key("o"))
	m = mm.(tuiModel)
	if cmd == nil {
		t.Fatal("o did not return a command")
	}
	m = send(t, m, cmd())
	if !slices.Equal(*opened, []string{"http://192.168.0.20/"}) || !strings.Contains(m.View(), "Opened http://192.168.0.20/") {
		t.Fatalf("opened=%v\n%s", *opened, m.View())
	}

	m = send(t, m, key("j")) // move to the second device
	_, cmd = m.Update(key("o"))
	cmd()
	if (*opened)[1] != "http://192.168.0.21:8080/" {
		t.Fatalf("opened=%v", *opened)
	}
}

func TestTUIOpenBrowserFromDetailUsesScheme(t *testing.T) {
	opened := stubOpenURL(t, errors.New("no browser"))
	m := newTUIModel(time.Second, time.Second)
	m.busy = ""
	m.screen = scrDetail
	m.ip = "192.168.0.20"
	m.dev = Device{Model: "P1455", Port: 80}
	m.client = NewClient(m.ip, "root", "pw", time.Second)
	m.client.scheme = "https"

	_, cmd := m.Update(key("o"))
	m = send(t, m, cmd())
	if !slices.Equal(*opened, []string{"https://192.168.0.20/"}) {
		t.Fatalf("opened=%v", *opened)
	}
	if m.err == nil || !strings.Contains(m.View(), "cannot open https://192.168.0.20/") {
		t.Fatalf("error not shown:\n%s", m.View())
	}
}
