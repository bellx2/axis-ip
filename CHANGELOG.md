# Changelog

## 0.3.3 - 2026-11-11

- Added Homebrew installation (`brew install bellx2/tap/axis-ip`) to the README.
- Added a release workflow: pushing a `v*` tag tests the code, creates the GitHub release and updates the Homebrew formula.

## 0.3.2 - 2026-11-11

- Changed the module path to `github.com/bellx2/axis-ip` so the tool can be installed with `go install github.com/bellx2/axis-ip@latest`.
- Added an MIT license.

## 0.3.1 - 2026-11-11

- The TUI device list now sizes the table to the number of devices instead of stretching it to the terminal height. It scrolls only when the devices do not fit.

## 0.3.0 - 2026-11-11

- Renamed the project and command from `axisip` to `axis-ip`. Build with `go build -o axis-ip .` and run `axis-ip` instead of `axisip`.

## 0.2.1 - 2026-11-11

- Switched all comments, messages, UI text and documentation to English for the open-source release.

## 0.2.0 - 2026-11-11

- Added a TUI. Running `axisip` with no arguments (or `axisip tui`) lets you scan, view `getNetworkInfo` and change IPv4 settings from one screen. It runs the same pre-write checks as `set`.
- Added Bubble Tea, Bubbles and Lip Gloss as dependencies.

## 0.1.0 - 2026-11-11

- `scan`: discover `_axis-video._tcp` through the DNS-SD API and print the model, MAC, hostname and IPv4 addresses.
- `get`: print the current IPv4 settings using `getNetworkInfo` from the Network Settings API.
- `set`: assign a static address or switch to DHCP with `setIPv4AddressConfiguration`. It verifies the credentials and current settings before writing and asks for confirmation.
- HTTP Digest authentication (MD5 and SHA-256) with fallback from HTTP to HTTPS.
