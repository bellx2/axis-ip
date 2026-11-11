package main

import (
	"net"
	"os/exec"
	"runtime"
	"strconv"
)

// openURL opens a URL in the default browser. Tests replace it.
var openURL = openBrowser

func openBrowser(url string) error {
	name, args := browserCommand(runtime.GOOS, url)
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap the launcher process
	return nil
}

// browserCommand returns the command that opens a URL on the given OS.
func browserCommand(goos, url string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		return "xdg-open", []string{url}
	}
}

// deviceURL returns the web interface URL of a device. The mDNS port applies
// to HTTP only; HTTPS uses the default port.
func deviceURL(ip string, port int, scheme string) string {
	if scheme == "https" {
		return "https://" + ip + "/"
	}
	host := ip
	if port != 0 && port != 80 {
		host = net.JoinHostPort(ip, strconv.Itoa(port))
	}
	return "http://" + host + "/"
}
