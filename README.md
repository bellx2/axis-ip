# axis-ip

English | [日本語](README_ja.md)

An Axis camera IP utility for macOS, with a CLI and a TUI. It discovers Axis devices on the local L2 segment over mDNS and changes their IPv4 address through the VAPIX Network Settings API.

The official AXIS IP Utility runs only on Windows. axis-ip covers the same everyday tasks on a Mac: find devices, read their network settings, and assign a static address or switch back to DHCP.

## Requirements

- macOS (discovery uses the system DNS-SD API through cgo)
- Go 1.26 or later and the Xcode Command Line Tools (cgo needs a C compiler)
- Axis devices running AXIS OS 8.50 or later (Network Settings API)

## Install

```sh
go install github.com/bellx2/axis-ip@latest
```

To build from a clone instead:

```sh
go build -o axis-ip .
```

The TUI is built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), Bubbles and Lip Gloss.

## Usage

### TUI

Run `axis-ip` with no arguments to open the TUI. It scans on startup; pick a device to read or change its address.

```sh
axis-ip [tui] [-timeout 2s] [-http-timeout 10s]
```

| Screen | Keys |
| --- | --- |
| Device list | `↑`/`↓` select, `enter` open, `r` rescan, `q` quit |
| Login | Enter the user (default `root`) and password, then `enter`. `AXIS_PASSWORD` pre-fills the password. `tab` switches fields, `esc` goes back |
| Detail | Shows the `getNetworkInfo` result. `e` edit, `r` reload, `esc` back |
| Edit | `space` on the mode field toggles static and DHCP. For static, enter the address as `IP/prefix` and an optional gateway. `tab`/`↑`/`↓` move, `enter` reviews |
| Confirm | Shows the current and new settings side by side. `y` writes, `n`/`esc` returns to the edit screen |

- The device table is only as tall as the number of devices and scrolls when they do not fit the terminal.
- axis-ip connects to the first IPv4 address from the scan, preferring non-link-local addresses.
- Once a device accepts a password, the TUI does not ask again until it exits.
- After the device accepts new settings, the TUI waits 5 seconds and rescans.

### scan

Browses `_axis-video._tcp` and prints the model, MAC, hostname and IPv4 addresses. Link-local addresses (169.254/16) are listed last.

```sh
axis-ip scan [-timeout 2s]
```

```
MODEL     MAC           HOST                     IPV4
P1455     ACCC8E000001  axis-accc8e000001.local  192.168.0.20, 169.254.10.20
```

### get

Sends `getNetworkInfo` to a device and prints the mode (dhcp or static), the addresses in use and the router.

```sh
axis-ip get -ip 192.168.0.20 [-user root]
```

### set

Writes a static address or switches back to DHCP. Before writing, axis-ip calls `getNetworkInfo` to verify the credentials and the target interface, shows the current and new settings, and asks for confirmation.

```sh
axis-ip set -ip 192.168.0.20 -address 192.168.0.90/24 [-gateway 192.168.0.1]
axis-ip set -ip 192.168.0.20 -dhcp
```

The device replies with success once it has validated the input, and changes the address afterwards. Losing contact with the old IP right after a successful reply is expected. If the change fails, the device reverts to its previous settings. Run `scan` to find the new address.

### Flags

| Flag | Applies to | Default | Description |
| --- | --- | --- | --- |
| `-timeout` | tui, scan | `2s` | Discovery duration |
| `-http-timeout` | tui | `10s` | HTTP timeout |
| `-ip` | get, set | none (required) | Current IP address or hostname of the device |
| `-user` | get, set | `root` | Administrator user name |
| `-password` | get, set | none | Password (`AXIS_PASSWORD` takes precedence) |
| `-timeout` | get, set | `10s` | HTTP timeout |
| `-address` | set | none | Static address as `IP/prefix` |
| `-gateway` | set | none | Default gateway, inside the `-address` subnet |
| `-dhcp` | set | `false` | Switch back to DHCP. Cannot be combined with `-address` |
| `-interface` | set | `eth0` | Target interface. Falls back to the first interface when `eth0` is absent |
| `-yes` | set | `false` | Skip the confirmation prompt |

Pass the IP with `-ip`, not as a positional argument. Leftover positional arguments are rejected so that a flag after them is never silently ignored.

### Password

axis-ip takes the password from, in order:

1. the `AXIS_PASSWORD` environment variable
2. `-password`
3. an interactive prompt (input is not echoed)

## How it talks to devices

- Discovery reads the mDNSResponder cache through `DNSServiceBrowse`, `DNSServiceResolve` and `DNSServiceGetAddrInfo`, so it does not bind UDP 5353 itself.
- Authentication is HTTP Digest (MD5 or SHA-256, qop=auth).
- axis-ip tries HTTP first and switches to HTTPS when HTTP is unreachable or redirects. Over HTTPS it accepts self-signed certificates.
- Writing requires that the Mac can reach the `-ip` address. For a device that only has a static address on another subnet, temporarily add an address in that subnet to a local interface first.

## Troubleshooting

- **`no route to host` although the device answers `curl`:** macOS Local Network privacy is blocking the binary. Enable your terminal app under System Settings > Privacy & Security > Local Network and restart it. Apple's own tools such as `curl` are exempt, so a working `curl` does not prove the permission is granted.
- **`DNSServiceBrowse failed -65563 (Service Not Running)`:** you are running inside a sandbox that blocks mDNSResponder. Run axis-ip outside the sandbox.

## License

MIT. See [LICENSE](LICENSE).
