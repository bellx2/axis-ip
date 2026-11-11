// axis-ip is an Axis camera IP utility for macOS and Linux.
// It discovers devices over mDNS and changes addresses via the VAPIX Network Settings API.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"
)

const version = "0.4.1"

const passwordEnv = "AXIS_PASSWORD"

// stdin is shared by the confirmation and password prompts; separate bufio readers would lose buffered input.
var stdin = bufio.NewReader(os.Stdin)

const usage = `Usage:
  axis-ip [tui] [-timeout 2s] [-http-timeout 10s]
  axis-ip scan [-timeout 2s]
  axis-ip get  -ip <IP> [-user root] [-password <pw>]
  axis-ip set  -ip <IP> [-user root] [-password <pw>] (-address <IP/prefix> [-gateway <IP>] | -dhcp) [-yes]
  axis-ip version

The password is taken from AXIS_PASSWORD, then -password, then an interactive prompt.
`

func main() {
	// Open the TUI when there are no arguments or the first argument is a flag.
	if len(os.Args) < 2 || (strings.HasPrefix(os.Args[1], "-") && !isMetaFlag(os.Args[1])) {
		os.Args = append([]string{os.Args[0], "tui"}, os.Args[1:]...)
	}
	var err error
	switch os.Args[1] {
	case "tui":
		err = cmdTUI(os.Args[2:])
	case "scan":
		err = cmdScan(os.Args[2:])
	case "get":
		err = cmdGet(os.Args[2:])
	case "set":
		err = cmdSet(os.Args[2:])
	case "version", "-version", "--version":
		fmt.Println("axis-ip", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func isMetaFlag(s string) bool {
	switch s {
	case "-h", "--help", "-version", "--version":
		return true
	}
	return false
}

func cmdTUI(args []string) error {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	timeout := fs.Duration("timeout", 2*time.Second, "discovery duration")
	httpTimeout := fs.Duration("http-timeout", 10*time.Second, "HTTP timeout")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	return runTUI(*timeout, *httpTimeout)
}

// parseFlags rejects leftover positional arguments so that flags such as -dhcp are never silently ignored.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s (pass the IP with -ip)", strings.Join(fs.Args(), " "))
	}
	return nil
}

func cmdScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	timeout := fs.Duration("timeout", 2*time.Second, "discovery duration")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	devs, err := scan(*timeout)
	if err != nil {
		return err
	}
	if len(devs) == 0 {
		fmt.Fprintln(os.Stderr, "no devices found")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "MODEL\tMAC\tHOST\tIPV4")
	for _, d := range devs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", d.Model, d.MAC, d.Host, strings.Join(d.IPv4, ", "))
	}
	return w.Flush()
}

// connFlags holds the connection flags shared by get and set.
type connFlags struct {
	ip, user, password *string
	timeout            *time.Duration
}

func addConnFlags(fs *flag.FlagSet) connFlags {
	return connFlags{
		ip:       fs.String("ip", "", "current IP address or hostname of the device (required)"),
		user:     fs.String("user", "root", "administrator user name"),
		password: fs.String("password", "", "password ("+passwordEnv+" takes precedence)"),
		timeout:  fs.Duration("timeout", 10*time.Second, "HTTP timeout"),
	}
}

func (f connFlags) client() (*Client, error) {
	if *f.ip == "" {
		return nil, errors.New("-ip is required")
	}
	pass := os.Getenv(passwordEnv)
	if pass == "" {
		pass = *f.password
	}
	if pass == "" {
		var err error
		if pass, err = readPassword(fmt.Sprintf("Password for %s@%s: ", *f.user, *f.ip)); err != nil {
			return nil, err
		}
	}
	return NewClient(*f.ip, *f.user, pass, *f.timeout), nil
}

func cmdGet(args []string) error {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	cf := addConnFlags(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	c, err := cf.client()
	if err != nil {
		return err
	}
	info, err := c.GetNetworkInfo()
	if err != nil {
		return err
	}
	for _, d := range info.Devices {
		fmt.Print(netDeviceText(d))
	}
	return nil
}

// netDeviceText formats the IPv4 settings of an interface for display.
func netDeviceText(d NetDevice) string {
	var b strings.Builder
	if d.MACAddress != "" {
		fmt.Fprintf(&b, "%s (%s)\n", d.Name, d.MACAddress)
	} else {
		fmt.Fprintln(&b, d.Name)
	}
	fmt.Fprintf(&b, "  Mode:          %s\n", d.IPv4.ConfigurationMode)
	for _, a := range d.IPv4.Addresses {
		origin := ""
		if a.Origin != "" {
			origin = " (" + a.Origin + ")"
		}
		fmt.Fprintf(&b, "  Address:       %s/%d%s\n", a.Address, a.PrefixLength, origin)
	}
	if d.IPv4.DefaultRouter != "" {
		fmt.Fprintf(&b, "  Router:        %s\n", d.IPv4.DefaultRouter)
	}
	if d.IPv4.ConfigurationMode == "static" {
		for _, a := range d.IPv4.StaticAddressConfigurations {
			fmt.Fprintf(&b, "  Static:        %s/%d\n", a.Address, a.PrefixLength)
		}
		if d.IPv4.StaticDefaultRouter != "" {
			fmt.Fprintf(&b, "  Static router: %s\n", d.IPv4.StaticDefaultRouter)
		}
	}
	return b.String()
}

// cfgText describes the settings that will be written.
func cfgText(c IPv4Config) string {
	if c.ConfigurationMode == "dhcp" {
		return "set " + c.DeviceName + " to DHCP"
	}
	a := c.StaticAddressConfigurations[0]
	s := fmt.Sprintf("set %s to static %s/%d", c.DeviceName, a.Address, a.PrefixLength)
	if c.StaticDefaultRouter != "" {
		s += ", gateway " + c.StaticDefaultRouter
	}
	return s
}

func cmdSet(args []string) error {
	fs := flag.NewFlagSet("set", flag.ContinueOnError)
	cf := addConnFlags(fs)
	address := fs.String("address", "", "new static address (e.g. 192.168.0.90/24)")
	gateway := fs.String("gateway", "", "default gateway (optional, static only)")
	dhcp := fs.Bool("dhcp", false, "switch back to DHCP")
	iface := fs.String("interface", "", "target interface (default eth0, or the first one if eth0 is absent)")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	cfg, err := buildIPv4Config(*address, *gateway, *dhcp)
	if err != nil {
		return err
	}
	c, err := cf.client()
	if err != nil {
		return err
	}

	// Verify credentials and the target interface before writing.
	info, err := c.GetNetworkInfo()
	if err != nil {
		return fmt.Errorf("cannot read the current settings, not writing: %w", err)
	}
	dev, err := pickDevice(info, *iface)
	if err != nil {
		return err
	}
	if dev.IPv4.ConfigurationMode == "" {
		return fmt.Errorf("cannot read the IPv4 settings of %s, not writing", dev.Name)
	}
	cfg.DeviceName = dev.Name

	fmt.Println("Current settings:")
	fmt.Print(netDeviceText(dev))
	fmt.Println("New settings:")
	fmt.Println("  " + cfgText(cfg))
	if !*yes && !confirm("Write these settings? [y/N]: ") {
		return errors.New("aborted")
	}

	if err := c.SetIPv4(cfg); err != nil {
		return err
	}
	fmt.Println("The device accepted the settings. The address changes shortly.")
	fmt.Println("If the change fails, the device reverts to its previous settings. Run scan to see the current address.")
	return nil
}

// buildIPv4Config validates the input and builds the settings. Exactly one of a static address or -dhcp is allowed.
func buildIPv4Config(address, gateway string, dhcp bool) (IPv4Config, error) {
	switch {
	case dhcp && (address != "" || gateway != ""):
		return IPv4Config{}, errors.New("-dhcp cannot be combined with -address or -gateway")
	case dhcp:
		return IPv4Config{ConfigurationMode: "dhcp"}, nil
	case address == "":
		return IPv4Config{}, errors.New("specify either -address or -dhcp")
	}
	p, err := netip.ParsePrefix(address)
	if err != nil {
		return IPv4Config{}, fmt.Errorf("address must look like 192.168.0.90/24: %v", err)
	}
	if !p.Addr().Is4() {
		return IPv4Config{}, errors.New("address must be IPv4")
	}
	cfg := IPv4Config{
		ConfigurationMode:           "static",
		StaticAddressConfigurations: []AddrConf{{Address: p.Addr().String(), PrefixLength: p.Bits()}},
	}
	if gateway != "" {
		g, err := netip.ParseAddr(gateway)
		if err != nil || !g.Is4() {
			return IPv4Config{}, fmt.Errorf("gateway is not an IPv4 address: %s", gateway)
		}
		if !p.Masked().Contains(g) {
			return IPv4Config{}, fmt.Errorf("gateway %s is outside %s", g, p.Masked())
		}
		cfg.StaticDefaultRouter = g.String()
	}
	return cfg, nil
}

func pickDevice(info *NetworkInfo, name string) (NetDevice, error) {
	if len(info.Devices) == 0 {
		return NetDevice{}, errors.New("getNetworkInfo returned no interfaces")
	}
	if name == "" {
		name = "eth0"
	}
	for _, d := range info.Devices {
		if d.Name == name {
			return d, nil
		}
	}
	if name == "eth0" {
		return info.Devices[0], nil
	}
	return NetDevice{}, fmt.Errorf("interface %s not found", name)
}

func confirm(prompt string) bool {
	fmt.Print(prompt)
	line, _ := stdin.ReadString('\n')
	ans := strings.ToLower(strings.TrimSpace(line))
	return ans == "y" || ans == "yes"
}

// readPassword reads a password from the terminal with echo disabled.
func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	stty := func(arg string) error {
		cmd := exec.Command("stty", arg)
		cmd.Stdin = os.Stdin
		return cmd.Run()
	}
	if err := stty("-echo"); err == nil {
		defer func() {
			stty("echo")
			fmt.Fprintln(os.Stderr)
		}()
	}
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("cannot read password: %v", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
