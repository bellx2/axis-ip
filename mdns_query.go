package main

// Pure Go mDNS discovery, used where the system DNS-SD API is not available.
//
// Queries are sent from an ephemeral port, so responders answer by unicast
// (RFC 6762 section 6.7, "legacy unicast"). The socket therefore never binds
// UDP 5353 and does not compete with avahi-daemon or systemd-resolved.

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/ipv4"
)

var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// mdnsResend is how long to wait before asking an unanswered question again.
const mdnsResend = 500 * time.Millisecond

type mdnsQuestion struct {
	name string
	typ  dnsmessage.Type
}

type srvRecord struct {
	host string
	port int
}

// mdnsCollector accumulates records from responses and decides which questions
// are still open. It does no I/O, so it can be tested with crafted packets.
type mdnsCollector struct {
	service string               // e.g. "_axis-video._tcp.local."
	ptrs    map[string]string    // lower-case instance name -> instance name
	srv     map[string]srvRecord // keyed by lower-case instance name
	txt     map[string]string    // lower-case instance name -> macaddress ("" if absent)
	addrs   map[string][]string  // lower-case hostname -> IPv4 addresses
	asked   map[mdnsQuestion]time.Time
}

func newMDNSCollector(service string) *mdnsCollector {
	return &mdnsCollector{
		service: service,
		ptrs:    map[string]string{},
		srv:     map[string]srvRecord{},
		txt:     map[string]string{},
		addrs:   map[string][]string{},
		asked:   map[mdnsQuestion]time.Time{},
	}
}

// handle records every PTR, SRV, TXT and A record in an mDNS response,
// whichever section it appears in.
func (c *mdnsCollector) handle(packet []byte) {
	var m dnsmessage.Message
	if err := m.Unpack(packet); err != nil || !m.Header.Response {
		return
	}
	for _, rrs := range [][]dnsmessage.Resource{m.Answers, m.Authorities, m.Additionals} {
		for _, rr := range rrs {
			if rr.Header.TTL == 0 {
				continue // goodbye record
			}
			key := strings.ToLower(rr.Header.Name.String())
			switch b := rr.Body.(type) {
			case *dnsmessage.PTRResource:
				if key == strings.ToLower(c.service) {
					name := b.PTR.String()
					c.ptrs[strings.ToLower(name)] = name
				}
			case *dnsmessage.SRVResource:
				c.srv[key] = srvRecord{host: b.Target.String(), port: int(b.Port)}
			case *dnsmessage.TXTResource:
				mac := c.txt[key]
				for _, s := range b.TXT {
					if v, ok := strings.CutPrefix(s, "macaddress="); ok {
						mac = v
					}
				}
				c.txt[key] = mac
			case *dnsmessage.AResource:
				c.addrs[key] = mergeAddrs(c.addrs[key], []string{net.IP(b.A[:]).String()})
			}
		}
	}
}

// pending lists the questions that still need an answer. The PTR question is
// always included so that devices answering late are still found.
func (c *mdnsCollector) pending() []mdnsQuestion {
	qs := []mdnsQuestion{{c.service, dnsmessage.TypePTR}}
	for key, name := range c.ptrs {
		if s, ok := c.srv[key]; !ok {
			qs = append(qs, mdnsQuestion{name, dnsmessage.TypeSRV})
		} else if len(c.addrs[strings.ToLower(s.host)]) == 0 {
			qs = append(qs, mdnsQuestion{s.host, dnsmessage.TypeA})
		}
		if _, ok := c.txt[key]; !ok {
			qs = append(qs, mdnsQuestion{name, dnsmessage.TypeTXT})
		}
	}
	return qs
}

// due returns the pending questions not asked within mdnsResend and marks them as asked.
func (c *mdnsCollector) due(now time.Time) []mdnsQuestion {
	var out []mdnsQuestion
	for _, q := range c.pending() {
		if t, ok := c.asked[q]; ok && now.Sub(t) < mdnsResend {
			continue
		}
		c.asked[q] = now
		out = append(out, q)
	}
	return out
}

func (c *mdnsCollector) state() *browseState {
	st := newBrowseState()
	for key, name := range c.ptrs {
		in := &instance{name: instanceName(name, c.service), mac: c.txt[key]}
		if s, ok := c.srv[key]; ok {
			in.host, in.port = s.host, s.port
			st.hosts[s.host] = c.addrs[strings.ToLower(s.host)]
		}
		st.instances[name] = in
	}
	return st
}

// instanceName strips the service suffix from a full instance name.
func instanceName(full, service string) string {
	if n := len(full) - len(service) - 1; n > 0 && strings.EqualFold(full[n+1:], service) && full[n] == '.' {
		return full[:n]
	}
	return full
}

func buildQuery(q mdnsQuestion) ([]byte, error) {
	name, err := dnsmessage.NewName(q.name)
	if err != nil {
		return nil, err
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: uint16(rand.Uint32())})
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(dnsmessage.Question{Name: name, Type: q.typ, Class: dnsmessage.ClassINET}); err != nil {
		return nil, err
	}
	return b.Finish()
}

// queryMDNS browses service (e.g. "_axis-video._tcp.local.") for the given duration.
func queryMDNS(service string, timeout time.Duration) (*browseState, error) {
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	pc := ipv4.NewPacketConn(conn)
	_ = pc.SetMulticastTTL(255)
	ifaces := multicastInterfaces()

	c := newMDNSCollector(service)
	everSent := false
	var lastErr error
	// send asks each due question on every multicast interface, one question per
	// packet because some responders ignore legacy unicast queries with several.
	send := func(now time.Time) {
		for _, q := range c.due(now) {
			msg, err := buildQuery(q)
			if err != nil {
				lastErr = err
				continue
			}
			if len(ifaces) == 0 {
				if _, err := conn.WriteTo(msg, mdnsGroup); err != nil {
					lastErr = err
				} else {
					everSent = true
				}
				continue
			}
			for i := range ifaces {
				if err := pc.SetMulticastInterface(&ifaces[i]); err != nil {
					lastErr = err
					continue
				}
				if _, err := conn.WriteTo(msg, mdnsGroup); err != nil {
					lastErr = err
					continue
				}
				everSent = true
			}
		}
	}

	start := time.Now()
	deadline := start.Add(timeout)
	send(start)
	if !everSent {
		return nil, fmt.Errorf("cannot send mDNS query: %w", lastErr)
	}
	buf := make([]byte, 9000)
	for {
		now := time.Now()
		if !now.Before(deadline) {
			break
		}
		// Wake up at least every mdnsResend to repeat unanswered questions.
		wake := now.Add(mdnsResend)
		if deadline.Before(wake) {
			wake = deadline
		}
		_ = conn.SetReadDeadline(wake)
		n, _, err := conn.ReadFromUDP(buf)
		var ne net.Error
		switch {
		case err == nil:
			c.handle(buf[:n])
		case errors.As(err, &ne) && ne.Timeout():
		default:
			return nil, err
		}
		send(time.Now())
	}
	return c.state(), nil
}

// multicastInterfaces returns the up, non-loopback, multicast-capable interfaces with an IPv4 address.
func multicastInterfaces() []net.Interface {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, ifi := range all {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				out = append(out, ifi)
				break
			}
		}
	}
	return out
}
