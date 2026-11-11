# axis-ip

axis-ip is an open-source Axis camera IP utility for macOS and Linux, written in Go. The official AXIS IP Utility is Windows-only; it finds devices on the same L2 segment and assigns IP addresses. axis-ip has a CLI and a TUI instead of a GUI, discovers devices over mDNS and changes addresses through VAPIX. The implementation is the Go package at the repository root; see README.md for usage. This file holds research notes.

## Language

This is an English open-source project. Write all code comments, user-facing messages, UI text, documentation and commit messages in English, even when the conversation is in another language.

The one exception is `README_ja.md`, a Japanese translation of `README.md`. Whenever `README.md` changes, update `README_ja.md` in the same change so the two stay equivalent, and keep the language links at the top of both files.

## What devices advertise

Instance names have the form `AXIS <model> - <MAC>`, and the TXT record holds only `macaddress=<12 hex digits>`. They resolve to `axis-<lowercase mac>.local.:80`. A device may advertise a link-local address alongside its regular one. The addresses come from mDNS A records and may change under DHCP.

`network_settings.cgi` answers an unauthenticated POST with a 401 carrying `Digest realm="AXIS_<MAC>", algorithm=MD5, qop="auth"`.

## Private notes

Notes about specific devices on the development LAN (MACs, addresses, test results) live in `CLAUDE.local.md`. That file is git-ignored and must never be published. Do not copy real MACs, IP addresses, hostnames or realms into tracked files; use placeholder values in docs and tests.

## Releasing

Pushing a `v*` tag runs `.github/workflows/release.yml`, which tests on Linux and macOS, creates the GitHub release from the matching CHANGELOG.md section, attaches static Linux binaries (amd64 and arm64) with checksums, points `Formula/axis-ip.rb` in `bellx2/homebrew-tap` at the new tarball, and installs it with Homebrew to verify. It needs the `TAP_GITHUB_TOKEN` secret (a token that can write to `bellx2/homebrew-tap`).

To release:

1. Set `const version` in `main.go` and add a `## <version> - <YYYY-MM-DD>` section to CHANGELOG.md. The workflow fails if the tag, `main.go` and CHANGELOG.md disagree.
2. Commit and push to `main`.
3. `git tag v<version> && git push origin v<version>`.

Do not edit the formula in the tap by hand for a normal release. The formula stays macOS-only; Linux is distributed as release binaries only.

`.github/workflows/ci.yml` runs vet and tests on macOS and Linux for every push to `main` and every pull request.

## Discovery

Use the service type `_axis-video._tcp.local.`. To include NVRs, also browse `_axis-nvr._tcp.local.`. The TXT `macaddress` identifies a device.

On macOS, mDNSResponder owns UDP 5353. `dns-sd` and the DNS-SD API (`DNSServiceBrowse` → `DNSServiceResolve` → `DNSServiceGetAddrInfo`) read its cache, so all three devices appeared within one second. Binding 5353 exclusively from a Go socket fails; to share the port, use `SO_REUSEPORT`.

Inside the Cursor sandbox, `dns-sd` fails with `DNSServiceBrowse failed -65563 (Service Not Running)`. Test discovery outside the sandbox.

On Linux, discovery is pure Go (`mdns_query.go`, selected by `mdns_other.go` on every non-darwin platform). It sends one question per packet from an ephemeral UDP port to 224.0.0.251:5353 on each multicast interface, and responders answer by unicast to that port (RFC 6762 section 6.7, legacy unicast). It walks PTR → SRV/TXT → A, repeating unanswered questions every 500 ms. This avoids binding 5353, so it coexists with avahi-daemon and systemd-resolved. Firewalls must let the unicast replies (source port 5353) in. The querier does not depend on Linux, so it can be checked from a Mac with `AXIS_IP_LIVE=1 go test -run TestQueryMDNSLive ./...`; on the development LAN it found all three devices within the default 2 seconds.

UDP 30718 is the Lantronix Discovery Protocol port and is not used for Axis discovery.

## Changing addresses

For reachable devices, change IPv4 with the Network Settings API (AXIS OS 8.50 and later). According to the documentation, this API replaces the Network group of `param.cgi`; on devices that have both, writing through either gives the same result.

The endpoint is `POST /axis-cgi/network_settings.cgi` with administrator Digest authentication. Two JSON `method` values are used:

- `getNetworkInfo` reads the interface name (`eth0` when wired), MAC, `configurationMode` (`dhcp` or `static`), addresses in use, prefix and router.
- `setIPv4AddressConfiguration` writes. For a static address, set `configurationMode` to `static`, put `address` and `prefixLength` in `staticAddressConfigurations`, and add `staticDefaultRouter` if needed. To return to DHCP, set `configurationMode` to `dhcp`.

The method returns success once the input is validated and switches the address afterwards. If the switch fails, the device reverts to the previous settings and records it in the system log. From the caller's side, the device may become unreachable at the same IP right after a successful reply.

The legacy parameters are `Network.BootProto` (`dhcp` / `none`), `Network.IPAddress`, `Network.SubnetMask` and `Network.DefaultRouter`, updated with `GET /axis-cgi/param.cgi?action=update&...`. The values in use live under `Network.eth0.*`, which is read-only.

Newer devices may prefer self-signed HTTPS. If HTTP is unreachable, try HTTPS.

As with the official tool, writing assumes the address seen during discovery is reachable. For a device with only a static address on another subnet, HTTP does not reach it even if mDNS reveals the IP. In that case, temporarily add an address in the target subnet to a local interface before calling VAPIX.

## Shape of the implementation

Discovery and changes are separate commands.

- `scan` prints the model, MAC, hostname and IPv4 from mDNS, listing global IPv4 before link-local.
- `get` sends `getNetworkInfo` to the given IP and prints the mode and addresses in use.
- `set` takes the current IP, user and password plus either a static address (address, prefix, gateway) or `-dhcp`. The environment variable takes precedence over the password flag, and nothing is written without first verifying the current settings.
- Running with no arguments opens the TUI, which combines all three.

The development environment is Go 1.26.4 darwin/arm64.
