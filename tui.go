package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// screen identifies the current TUI screen.
type screen int

const (
	scrList    screen = iota // device list
	scrLogin                 // user name and password
	scrDetail                // getNetworkInfo result
	scrEdit                  // new settings input
	scrConfirm               // write confirmation
)

// Results of asynchronous operations.
type (
	scanDoneMsg struct {
		devs []Device
		err  error
	}
	infoDoneMsg struct {
		info *NetworkInfo
		err  error
	}
	setDoneMsg  struct{ err error }
	openDoneMsg struct {
		url string
		err error
	}
	waitDoneMsg struct{} // the post-write wait for the address change has elapsed
)

type creds struct{ user, pass string }

// Fields on the edit screen.
const (
	fieldMode = iota
	fieldAddress
	fieldGateway
	fieldCount
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	helpStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	focusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
)

type tuiModel struct {
	screen  screen
	busy    string // in-progress label; empty when idle
	status  string // result message of the last operation
	err     error
	spinner spinner.Model

	termHeight  int // 0 until the first WindowSizeMsg
	scanTimeout time.Duration
	httpTimeout time.Duration

	devs  []Device
	table table.Model

	// Selected device.
	dev    Device
	ip     string
	client *Client
	creds  map[string]creds // verified credentials per IP
	login  [2]textinput.Model
	loginF int
	net    NetDevice

	// Edit screen.
	dhcp   bool
	edit   [fieldCount]textinput.Model // fieldMode is unused
	editF  int
	newCfg IPv4Config
}

func runTUI(scanTimeout, httpTimeout time.Duration) error {
	m := newTUIModel(scanTimeout, httpTimeout)
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func newTUIModel(scanTimeout, httpTimeout time.Duration) tuiModel {
	t := table.New(
		table.WithColumns([]table.Column{
			{Title: "MODEL", Width: 12}, {Title: "MAC", Width: 14},
			{Title: "HOST", Width: 26}, {Title: "IPV4", Width: 34},
		}),
		table.WithFocused(true),
		table.WithHeight(10),
	)
	user := textinput.New()
	user.Prompt = "User:     "
	user.SetValue("root")
	pass := textinput.New()
	pass.Prompt = "Password: "
	pass.EchoMode = textinput.EchoPassword
	pass.SetValue(os.Getenv(passwordEnv))

	var edit [fieldCount]textinput.Model
	edit[fieldAddress] = textinput.New()
	edit[fieldAddress].Prompt = "Address: "
	edit[fieldAddress].Placeholder = "192.168.0.90/24"
	edit[fieldGateway] = textinput.New()
	edit[fieldGateway].Prompt = "Gateway: "
	edit[fieldGateway].Placeholder = "(optional)"

	return tuiModel{
		scanTimeout: scanTimeout, httpTimeout: httpTimeout,
		spinner: spinner.New(spinner.WithSpinner(spinner.Dot)),
		table:   t,
		creds:   map[string]creds{},
		login:   [2]textinput.Model{user, pass},
		edit:    edit,
		busy:    "Scanning",
	}
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.scanCmd())
}

func (m tuiModel) scanCmd() tea.Cmd {
	timeout := m.scanTimeout
	return func() tea.Msg {
		devs, err := scan(timeout)
		return scanDoneMsg{devs, err}
	}
}

func (m tuiModel) getCmd() tea.Cmd {
	c := m.client
	return func() tea.Msg {
		info, err := c.GetNetworkInfo()
		return infoDoneMsg{info, err}
	}
}

func (m tuiModel) setCmd() tea.Cmd {
	c, cfg := m.client, m.newCfg
	return func() tea.Msg { return setDoneMsg{c.SetIPv4(cfg)} }
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.busy != "" {
			return m, nil // ignore keys while an operation is running
		}
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.termHeight = msg.Height
		m.fitTable()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case scanDoneMsg:
		m.busy = ""
		m.err = msg.err
		m.devs = msg.devs
		rows := make([]table.Row, len(msg.devs))
		for i, d := range msg.devs {
			rows[i] = table.Row{d.Model, d.MAC, d.Host, strings.Join(d.IPv4, ", ")}
		}
		m.table.SetRows(rows)
		m.fitTable()
		if m.table.Cursor() >= len(rows) {
			m.table.SetCursor(max(0, len(rows)-1))
		}
		return m, nil

	case infoDoneMsg:
		m.busy = ""
		if msg.err != nil {
			// Drop the credentials and return to the login screen.
			delete(m.creds, m.ip)
			m.err = msg.err
			return m, m.showLogin()
		}
		dev, err := pickDevice(msg.info, "")
		if err != nil {
			m.err = err
			m.screen = scrList
			return m, nil
		}
		m.creds[m.ip] = creds{m.client.User, m.client.Pass}
		m.net, m.err, m.screen = dev, nil, scrDetail
		return m, nil

	case setDoneMsg:
		m.busy = ""
		if msg.err != nil {
			m.err = msg.err
			m.screen = scrConfirm
			return m, nil
		}
		// The old IP may stop working, so forget its credentials.
		delete(m.creds, m.ip)
		m.err = nil
		m.status = fmt.Sprintf("%s accepted the settings. Rescanning after the address change.", m.dev.Model)
		m.screen = scrList
		m.busy = "Waiting for the address change, then rescanning"
		wait := tea.Tick(5*time.Second, func(time.Time) tea.Msg { return waitDoneMsg{} })
		return m, tea.Batch(m.spinner.Tick, wait)

	case waitDoneMsg:
		return m, m.scanCmd()

	case openDoneMsg:
		if msg.err != nil {
			m.err = fmt.Errorf("cannot open %s: %v", msg.url, msg.err)
		} else {
			m.err, m.status = nil, "Opened "+msg.url+" in the browser"
		}
		return m, nil
	}
	return m, nil
}

func (m tuiModel) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.screen {
	case scrList:
		switch k.String() {
		case "q":
			return m, tea.Quit
		case "r":
			m.busy, m.err, m.status = "Scanning", nil, ""
			return m, tea.Batch(m.spinner.Tick, m.scanCmd())
		case "o":
			if len(m.devs) == 0 {
				return m, nil
			}
			d := m.devs[m.table.Cursor()]
			if len(d.IPv4) == 0 {
				m.err = fmt.Errorf("no IPv4 address known for %s", d.Model)
				return m, nil
			}
			return m, openCmd(deviceURL(d.IPv4[0], d.Port, ""))
		case "enter":
			if len(m.devs) == 0 {
				return m, nil
			}
			m.dev = m.devs[m.table.Cursor()]
			if len(m.dev.IPv4) == 0 {
				m.err = fmt.Errorf("no IPv4 address known for %s", m.dev.Model)
				return m, nil
			}
			m.ip, m.err, m.status = m.dev.IPv4[0], nil, ""
			if c, ok := m.creds[m.ip]; ok {
				return m.startGet(c)
			}
			return m, m.showLogin()
		}
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(k)
		return m, cmd

	case scrLogin:
		switch k.String() {
		case "esc":
			m.screen, m.err = scrList, nil
			return m, nil
		case "tab", "shift+tab", "up", "down":
			m.login[m.loginF].Blur()
			m.loginF = 1 - m.loginF
			return m, m.login[m.loginF].Focus()
		case "enter":
			c := creds{strings.TrimSpace(m.login[0].Value()), m.login[1].Value()}
			if c.user == "" || c.pass == "" {
				m.err = fmt.Errorf("enter a user name and password")
				return m, nil
			}
			return m.startGet(c)
		}
		var cmd tea.Cmd
		m.login[m.loginF], cmd = m.login[m.loginF].Update(k)
		return m, cmd

	case scrDetail:
		switch k.String() {
		case "q":
			return m, tea.Quit
		case "esc":
			m.screen = scrList
			return m, nil
		case "r":
			return m.startGet(m.creds[m.ip])
		case "o":
			return m, openCmd(deviceURL(m.ip, m.dev.Port, m.client.scheme))
		case "e":
			if m.net.IPv4.ConfigurationMode == "" {
				m.err = fmt.Errorf("cannot read the IPv4 settings of %s, editing is disabled", m.net.Name)
				return m, nil
			}
			return m, m.showEdit()
		}
		return m, nil

	case scrEdit:
		switch k.String() {
		case "esc":
			m.screen, m.err = scrDetail, nil
			return m, nil
		case "tab", "down":
			return m, m.focusEdit(m.nextEditField(1))
		case "shift+tab", "up":
			return m, m.focusEdit(m.nextEditField(-1))
		case "enter":
			addr, gw := m.edit[fieldAddress].Value(), m.edit[fieldGateway].Value()
			if m.dhcp {
				addr, gw = "", ""
			}
			cfg, err := buildIPv4Config(strings.TrimSpace(addr), strings.TrimSpace(gw), m.dhcp)
			if err != nil {
				m.err = err
				return m, nil
			}
			cfg.DeviceName = m.net.Name
			m.newCfg, m.err, m.screen = cfg, nil, scrConfirm
			return m, nil
		}
		if m.editF == fieldMode {
			switch k.String() {
			case " ", "left", "right", "h", "l":
				m.dhcp = !m.dhcp
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.edit[m.editF], cmd = m.edit[m.editF].Update(k)
		return m, cmd

	case scrConfirm:
		switch k.String() {
		case "y", "Y":
			m.busy, m.err = "Writing", nil
			return m, tea.Batch(m.spinner.Tick, m.setCmd())
		case "n", "N", "esc":
			m.screen, m.err = scrEdit, nil
			return m, nil
		}
	}
	return m, nil
}

// fitTable sizes the device table to its rows instead of filling the terminal,
// and only scrolls when the rows do not fit.
func (m *tuiModel) fitTable() {
	// SetHeight counts the header lines too; measure them once.
	m.table.SetHeight(100)
	header := 100 - m.table.Height()
	h := header + max(1, len(m.table.Rows()))
	if m.termHeight > 0 {
		h = min(h, max(header+1, m.termHeight-8))
	}
	m.table.SetHeight(h)
}

// openCmd opens url in the browser without blocking the UI.
func openCmd(url string) tea.Cmd {
	return func() tea.Msg { return openDoneMsg{url, openURL(url)} }
}

func (m tuiModel) startGet(c creds) (tea.Model, tea.Cmd) {
	m.client = NewClient(m.ip, c.user, c.pass, m.httpTimeout)
	m.busy = "Loading"
	return m, tea.Batch(m.spinner.Tick, m.getCmd())
}

func (m *tuiModel) showLogin() tea.Cmd {
	m.screen = scrLogin
	m.login[0].Blur()
	m.loginF = 1
	return m.login[1].Focus()
}

// showEdit opens the edit screen pre-filled with the current settings.
func (m *tuiModel) showEdit() tea.Cmd {
	ip := m.net.IPv4
	m.dhcp = ip.ConfigurationMode == "dhcp"
	addr, gw := "", ip.DefaultRouter
	if ip.ConfigurationMode == "static" && len(ip.StaticAddressConfigurations) > 0 {
		a := ip.StaticAddressConfigurations[0]
		addr, gw = a.Address+"/"+strconv.Itoa(a.PrefixLength), ip.StaticDefaultRouter
	} else if len(ip.Addresses) > 0 {
		a := ip.Addresses[0]
		addr = a.Address + "/" + strconv.Itoa(a.PrefixLength)
	}
	m.edit[fieldAddress].SetValue(addr)
	m.edit[fieldGateway].SetValue(gw)
	m.screen, m.err = scrEdit, nil
	return m.focusEdit(fieldMode)
}

// nextEditField skips the address fields in DHCP mode.
func (m tuiModel) nextEditField(d int) int {
	if m.dhcp {
		return fieldMode
	}
	return (m.editF + d + fieldCount) % fieldCount
}

func (m *tuiModel) focusEdit(f int) tea.Cmd {
	m.edit[fieldAddress].Blur()
	m.edit[fieldGateway].Blur()
	m.editF = f
	if f == fieldMode {
		return nil
	}
	return m.edit[f].Focus()
}

func (m tuiModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("axis-ip "+version) + helpStyle.Render(" by bellx2") + "\n\n")

	switch m.screen {
	case scrList:
		if len(m.devs) == 0 && m.busy == "" {
			b.WriteString("No devices found\n")
		} else {
			b.WriteString(m.table.View() + "\n")
		}
	case scrLogin:
		fmt.Fprintf(&b, "%s  %s\n\n", m.devLabel(), m.ip)
		b.WriteString(m.login[0].View() + "\n" + m.login[1].View() + "\n")
	case scrDetail:
		fmt.Fprintf(&b, "%s  %s\n\n", m.devLabel(), m.ip)
		b.WriteString(netDeviceText(m.net))
	case scrEdit:
		fmt.Fprintf(&b, "%s  %s  %s\n\n", m.devLabel(), m.ip, m.net.Name)
		mode := "[ Static ]  DHCP"
		if m.dhcp {
			mode = "  Static  [ DHCP ]"
		}
		label := "Mode:    "
		if m.editF == fieldMode {
			label = focusStyle.Render(label)
		}
		b.WriteString(label + mode + "\n")
		if !m.dhcp {
			b.WriteString(m.edit[fieldAddress].View() + "\n" + m.edit[fieldGateway].View() + "\n")
		}
	case scrConfirm:
		fmt.Fprintf(&b, "%s  %s\n\n", m.devLabel(), m.ip)
		b.WriteString("Current settings:\n" + netDeviceText(m.net) + "\nNew settings:\n  " + cfgText(m.newCfg) + "\n\n")
		b.WriteString("Write these settings? [y/N]\n")
	}

	b.WriteString("\n")
	if m.busy != "" {
		b.WriteString(m.spinner.View() + " " + m.busy + "\n")
	}
	if m.err != nil {
		b.WriteString(errStyle.Render("Error: "+m.err.Error()) + "\n")
	}
	if m.status != "" {
		b.WriteString(okStyle.Render(m.status) + "\n")
	}
	b.WriteString(helpStyle.Render(m.help()) + "\n")
	return b.String()
}

func (m tuiModel) devLabel() string {
	return fmt.Sprintf("AXIS %s (%s)", m.dev.Model, m.dev.MAC)
}

func (m tuiModel) help() string {
	switch m.screen {
	case scrList:
		return "↑/↓ select  enter open  o browser  r rescan  q quit"
	case scrLogin:
		return "tab switch field  enter connect  esc back"
	case scrDetail:
		return "e edit  o browser  r reload  esc back  q quit"
	case scrEdit:
		return "tab/↑/↓ move  space toggle mode  enter review  esc back"
	case scrConfirm:
		return "y write  n/esc back"
	}
	return ""
}
