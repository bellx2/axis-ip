package main

import (
	"net/netip"
	"sort"
	"strings"
	"time"
)

const serviceType = "_axis-video._tcp"

// Device is a device discovered over mDNS.
type Device struct {
	Model string
	MAC   string
	Host  string
	Port  int
	IPv4  []string
}

// scan browses _axis-video._tcp and returns the devices, deduplicated by MAC.
func scan(timeout time.Duration) ([]Device, error) {
	st, err := browseRaw(serviceType, timeout)
	if err != nil {
		return nil, err
	}
	return collect(st), nil
}

// collect turns browse results into a device list, merging a device seen on several interfaces.
func collect(st *browseState) []Device {
	byKey := map[string]*Device{}
	var order []string
	for _, in := range st.instances {
		model, nameMAC := parseInstanceName(in.name)
		mac := strings.ToUpper(in.mac)
		if mac == "" {
			mac = nameMAC
		}
		key := mac
		if key == "" {
			key = in.name
		}
		d, ok := byKey[key]
		if !ok {
			d = &Device{Model: model, MAC: mac}
			byKey[key] = d
			order = append(order, key)
		}
		if d.Host == "" && in.host != "" {
			d.Host = strings.TrimSuffix(in.host, ".")
			d.Port = in.port
		}
		d.IPv4 = mergeAddrs(d.IPv4, st.hosts[in.host])
	}
	devs := make([]Device, 0, len(order))
	for _, k := range order {
		d := byKey[k]
		sortAddrs(d.IPv4)
		devs = append(devs, *d)
	}
	sort.Slice(devs, func(i, j int) bool { return devs[i].Model+devs[i].MAC < devs[j].Model+devs[j].MAC })
	return devs
}

// parseInstanceName splits "AXIS <model> - <MAC>" into the model and MAC.
func parseInstanceName(name string) (model, mac string) {
	s := strings.TrimPrefix(name, "AXIS ")
	if i := strings.LastIndex(s, " - "); i >= 0 {
		return s[:i], strings.ToUpper(s[i+3:])
	}
	return s, ""
}

func mergeAddrs(dst, src []string) []string {
	for _, a := range src {
		dup := false
		for _, b := range dst {
			if a == b {
				dup = true
				break
			}
		}
		if !dup {
			dst = append(dst, a)
		}
	}
	return dst
}

// sortAddrs moves link-local addresses (169.254/16) to the end.
func sortAddrs(addrs []string) {
	sort.SliceStable(addrs, func(i, j int) bool {
		return !isLinkLocal(addrs[i]) && isLinkLocal(addrs[j])
	})
}

func isLinkLocal(s string) bool {
	a, err := netip.ParseAddr(s)
	return err == nil && a.IsLinkLocalUnicast()
}
