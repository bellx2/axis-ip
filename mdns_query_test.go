package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const testService = "_axis-video._tcp.local."

// response builds an mDNS response packet from the given resources.
func response(t *testing.T, answers []dnsmessage.Resource, additionals ...dnsmessage.Resource) []byte {
	t.Helper()
	m := dnsmessage.Message{
		Header:      dnsmessage.Header{Response: true, Authoritative: true},
		Answers:     answers,
		Additionals: additionals,
	}
	b, err := m.Pack()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func rrHeader(name string, typ dnsmessage.Type) dnsmessage.ResourceHeader {
	return dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(name), Type: typ, Class: dnsmessage.ClassINET, TTL: 120}
}

func ptrRR(target string) dnsmessage.Resource {
	return dnsmessage.Resource{Header: rrHeader(testService, dnsmessage.TypePTR), Body: &dnsmessage.PTRResource{PTR: dnsmessage.MustNewName(target)}}
}

func srvRR(name, host string) dnsmessage.Resource {
	return dnsmessage.Resource{Header: rrHeader(name, dnsmessage.TypeSRV), Body: &dnsmessage.SRVResource{Target: dnsmessage.MustNewName(host), Port: 80}}
}

func txtRR(name string, txt ...string) dnsmessage.Resource {
	return dnsmessage.Resource{Header: rrHeader(name, dnsmessage.TypeTXT), Body: &dnsmessage.TXTResource{TXT: txt}}
}

func aRR(host string, a [4]byte) dnsmessage.Resource {
	return dnsmessage.Resource{Header: rrHeader(host, dnsmessage.TypeA), Body: &dnsmessage.AResource{A: a}}
}

func hasQuestion(qs []mdnsQuestion, name string, typ dnsmessage.Type) bool {
	for _, q := range qs {
		if strings.EqualFold(q.name, name) && q.typ == typ {
			return true
		}
	}
	return false
}

// Each answer opens the next question: PTR -> SRV/TXT -> A.
func TestMDNSCollectorStepByStep(t *testing.T) {
	const inst = "AXIS P1455 - ACCC8E000001." + testService
	const host = "axis-accc8e000001.local."
	c := newMDNSCollector(testService)

	if qs := c.pending(); len(qs) != 1 || qs[0].typ != dnsmessage.TypePTR {
		t.Fatalf("initial questions: %+v", qs)
	}

	c.handle(response(t, []dnsmessage.Resource{ptrRR(inst)}))
	qs := c.pending()
	if !hasQuestion(qs, inst, dnsmessage.TypeSRV) || !hasQuestion(qs, inst, dnsmessage.TypeTXT) {
		t.Fatalf("after PTR: %+v", qs)
	}

	c.handle(response(t, []dnsmessage.Resource{srvRR(inst, host), txtRR(inst, "macaddress=ACCC8E000001")}))
	qs = c.pending()
	if !hasQuestion(qs, host, dnsmessage.TypeA) || hasQuestion(qs, inst, dnsmessage.TypeSRV) || hasQuestion(qs, inst, dnsmessage.TypeTXT) {
		t.Fatalf("after SRV/TXT: %+v", qs)
	}

	c.handle(response(t, []dnsmessage.Resource{aRR(host, [4]byte{192, 168, 0, 20})}))
	c.handle(response(t, []dnsmessage.Resource{aRR(host, [4]byte{169, 254, 10, 20})}))
	if qs := c.pending(); len(qs) != 1 {
		t.Fatalf("questions left after A: %+v", qs)
	}

	devs := collect(c.state())
	if len(devs) != 1 {
		t.Fatalf("%+v", devs)
	}
	d := devs[0]
	if d.Model != "P1455" || d.MAC != "ACCC8E000001" || d.Host != "axis-accc8e000001.local" || d.Port != 80 ||
		strings.Join(d.IPv4, ",") != "192.168.0.20,169.254.10.20" {
		t.Fatalf("%+v", d)
	}
}

// Records in the additional section are used as well, and names match case-insensitively.
func TestMDNSCollectorAdditionalsAndCase(t *testing.T) {
	const inst = "AXIS M3086 - ACCC8E000002." + testService
	c := newMDNSCollector(testService)
	c.handle(response(t,
		[]dnsmessage.Resource{ptrRR(inst)},
		srvRR(strings.ToLower(inst), "AXIS-ACCC8E000002.local."),
		txtRR(inst, "macaddress=accc8e000002"),
		aRR("axis-accc8e000002.local.", [4]byte{192, 168, 0, 21}),
	))
	if qs := c.pending(); len(qs) != 1 {
		t.Fatalf("questions left: %+v", qs)
	}
	devs := collect(c.state())
	if len(devs) != 1 || devs[0].MAC != "ACCC8E000002" || strings.Join(devs[0].IPv4, ",") != "192.168.0.21" {
		t.Fatalf("%+v", devs)
	}
}

// PTR records for other services, goodbye records and queries are ignored.
func TestMDNSCollectorIgnores(t *testing.T) {
	c := newMDNSCollector(testService)
	other := dnsmessage.Resource{
		Header: rrHeader("_http._tcp.local.", dnsmessage.TypePTR),
		Body:   &dnsmessage.PTRResource{PTR: dnsmessage.MustNewName("printer._http._tcp.local.")},
	}
	bye := ptrRR("AXIS P1455 - ACCC8E000001." + testService)
	bye.Header.TTL = 0
	c.handle(response(t, []dnsmessage.Resource{other, bye}))

	query := dnsmessage.Message{Answers: []dnsmessage.Resource{ptrRR("AXIS P1455 - ACCC8E000003." + testService)}}
	b, _ := query.Pack()
	c.handle(b)
	c.handle([]byte{0x01, 0x02}) // malformed

	if len(c.ptrs) != 0 {
		t.Fatalf("ptrs = %+v", c.ptrs)
	}
}

func TestMDNSCollectorDue(t *testing.T) {
	c := newMDNSCollector(testService)
	now := time.Now()
	if len(c.due(now)) != 1 {
		t.Fatal("PTR not due at start")
	}
	if len(c.due(now.Add(mdnsResend/2))) != 0 {
		t.Fatal("PTR repeated too early")
	}
	if len(c.due(now.Add(mdnsResend))) != 1 {
		t.Fatal("PTR not repeated after mdnsResend")
	}
}

func TestBuildQuery(t *testing.T) {
	b, err := buildQuery(mdnsQuestion{"AXIS P1455 - ACCC8E000001." + testService, dnsmessage.TypeTXT})
	if err != nil {
		t.Fatal(err)
	}
	var m dnsmessage.Message
	if err := m.Unpack(b); err != nil {
		t.Fatal(err)
	}
	if m.Header.Response || len(m.Questions) != 1 || m.Questions[0].Type != dnsmessage.TypeTXT ||
		m.Questions[0].Name.String() != "AXIS P1455 - ACCC8E000001."+testService {
		t.Fatalf("%+v", m)
	}
}

func TestInstanceName(t *testing.T) {
	for full, want := range map[string]string{
		"AXIS P1455 - ACCC8E000001." + testService:          "AXIS P1455 - ACCC8E000001",
		"AXIS P1455 - ACCC8E000001._AXIS-VIDEO._TCP.LOCAL.": "AXIS P1455 - ACCC8E000001",
		testService:  testService,
		"unrelated.": "unrelated.",
	} {
		if got := instanceName(full, testService); got != want {
			t.Errorf("instanceName(%q) = %q, want %q", full, got, want)
		}
	}
}

// TestQueryMDNSLive queries the real network. Run with AXIS_IP_LIVE=1.
func TestQueryMDNSLive(t *testing.T) {
	if os.Getenv("AXIS_IP_LIVE") == "" {
		t.Skip("set AXIS_IP_LIVE=1 to query the local network")
	}
	st, err := queryMDNS(serviceType+".local.", 2*time.Second) // the default scan timeout
	if err != nil {
		t.Fatal(err)
	}
	devs := collect(st)
	for _, d := range devs {
		t.Logf("%s %s %s %v", d.Model, d.MAC, d.Host, d.IPv4)
	}
	if len(devs) == 0 {
		t.Fatal("no devices found")
	}
}
