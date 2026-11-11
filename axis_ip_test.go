package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Test vectors from RFC 7616 section 3.9.1.
func TestDigestRFC7616(t *testing.T) {
	base := digestChallenge{
		realm: "http-auth@example.org", qop: "auth",
		nonce:  "7ypf/xlj9XXwfDPEoM4URrv/xwf94BcCAzFZH4GiTo0v",
		opaque: "FQhe/qaU925kfnzjCev0ciny7QMkPqMAFRtzCUYo5tdS",
	}
	cnonce := "f2/wE4q74E6zIJEtWaHKaf5wv/H5QzzpXusqGemxURZJ"
	for alg, want := range map[string]string{
		"MD5":     "8ca523f5e9506fed4657c9700eebdbec",
		"SHA-256": "753927fa0e85d155564e2e272a28d1802ca10daf4496794697cf8db5856cb6c1",
	} {
		c := base
		c.algorithm = alg
		got, err := digestAuthorization(c, "Mufasa", "Circle of Life", "GET", "/dir/index.html", 1, cnonce)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, `response="`+want+`"`) {
			t.Errorf("%s: %s", alg, got)
		}
	}
}

func TestPickChallenge(t *testing.T) {
	cs := parseChallenges([]string{
		`Basic realm="x"`,
		`Digest realm="AXIS_ACCC8E000001", nonce="dGVzdG5vbmNl", algorithm=MD5, qop="auth"`,
		`Digest realm="r", nonce="n2", algorithm=SHA-256, qop="auth"`,
	})
	c, ok := pickChallenge(cs)
	if !ok || c.algorithm != "SHA-256" {
		t.Fatalf("%+v", c)
	}
	c, ok = pickChallenge(cs[:1])
	if !ok || c.realm != "AXIS_ACCC8E000001" || c.nonce != "dGVzdG5vbmNl" || c.qop != "auth" {
		t.Fatalf("%+v", c)
	}
}

func TestBuildIPv4Config(t *testing.T) {
	cfg, err := buildIPv4Config("192.168.0.90/24", "192.168.0.1", false)
	if err != nil || cfg.ConfigurationMode != "static" || cfg.StaticDefaultRouter != "192.168.0.1" ||
		cfg.StaticAddressConfigurations[0] != (AddrConf{Address: "192.168.0.90", PrefixLength: 24}) {
		t.Fatalf("%+v %v", cfg, err)
	}
	if cfg, err := buildIPv4Config("", "", true); err != nil || cfg.ConfigurationMode != "dhcp" {
		t.Fatalf("%+v %v", cfg, err)
	}
	for _, bad := range []struct {
		addr, gw string
		dhcp     bool
	}{
		{"", "", false},
		{"192.168.0.90/24", "", true},
		{"192.168.0.90", "", false},
		{"192.168.0.90/33", "", false},
		{"fe80::1/64", "", false},
		{"192.168.0.90/24", "10.0.0.1", false},
	} {
		if _, err := buildIPv4Config(bad.addr, bad.gw, bad.dhcp); err == nil {
			t.Errorf("expected an error: %+v", bad)
		}
	}
}

func TestCollect(t *testing.T) {
	st := &browseState{
		instances: map[string]*instance{
			"a1": {name: "AXIS P1455 - ACCC8E000001", ifIndex: 12, host: "axis-accc8e000001.local.", port: 80, mac: "ACCC8E000001"},
			// the same device seen on another interface
			"a2": {name: "AXIS P1455 - ACCC8E000001", ifIndex: 14, host: "axis-accc8e000001.local.", port: 80, mac: "accc8e000001"},
		},
		hosts: map[string][]string{"axis-accc8e000001.local.": {"169.254.10.20", "192.168.0.20"}},
	}
	devs := collect(st)
	if len(devs) != 1 {
		t.Fatalf("%+v", devs)
	}
	d := devs[0]
	if d.Model != "P1455" || d.MAC != "ACCC8E000001" || d.Host != "axis-accc8e000001.local" ||
		strings.Join(d.IPv4, ",") != "192.168.0.20,169.254.10.20" {
		t.Fatalf("%+v", d)
	}
}

// fakeCamera emulates network_settings.cgi behind Digest authentication.
type fakeCamera struct {
	user, pass string
	lastSet    map[string]any
}

func (f *fakeCamera) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const realm, nonce = "AXIS_TEST", "abc123"
	if !f.checkAuth(r, realm, nonce) {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Digest realm="%s", nonce="%s", algorithm=MD5, qop="auth"`, realm, nonce))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var req map[string]any
	json.Unmarshal(body, &req)
	switch req["method"] {
	case "getNetworkInfo":
		io.WriteString(w, `{"apiVersion":"1.0","method":"getNetworkInfo","data":{"devices":[{"name":"eth0","macAddress":"ac:cc:8e:00:00:01","IPv4":{"enabled":true,"configurationMode":"dhcp","addresses":[{"address":"192.168.0.20","prefixLength":24,"origin":"dhcp"}],"defaultRouter":"192.168.0.1"}}]}}`)
	case "setIPv4AddressConfiguration":
		f.lastSet, _ = req["params"].(map[string]any)
		io.WriteString(w, `{"apiVersion":"1.0","method":"setIPv4AddressConfiguration","data":{}}`)
	default:
		io.WriteString(w, `{"apiVersion":"1.0","error":{"code":2002,"message":"Method not supported"}}`)
	}
}

func (f *fakeCamera) checkAuth(r *http.Request, realm, nonce string) bool {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Digest ") {
		return false
	}
	p := parseAuthParams(h[7:])
	md := func(s string) string { x := md5.Sum([]byte(s)); return hex.EncodeToString(x[:]) }
	ha1 := md(f.user + ":" + realm + ":" + f.pass)
	ha2 := md(r.Method + ":" + p["uri"])
	want := md(ha1 + ":" + nonce + ":" + p["nc"] + ":" + p["cnonce"] + ":" + p["qop"] + ":" + ha2)
	return p["username"] == f.user && p["nonce"] == nonce && p["response"] == want
}

func TestClientGetSet(t *testing.T) {
	cam := &fakeCamera{user: "root", pass: "secret"}
	srv := httptest.NewServer(cam)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	c := NewClient(host, "root", "secret", 5*time.Second)
	info, err := c.GetNetworkInfo()
	if err != nil {
		t.Fatal(err)
	}
	d, err := pickDevice(info, "")
	if err != nil || d.Name != "eth0" || d.IPv4.ConfigurationMode != "dhcp" || d.IPv4.Addresses[0].Address != "192.168.0.20" {
		t.Fatalf("%+v %v", d, err)
	}

	cfg, _ := buildIPv4Config("192.168.0.90/24", "192.168.0.1", false)
	cfg.DeviceName = d.Name
	if err := c.SetIPv4(cfg); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(cam.lastSet)
	want := `{"configurationMode":"static","deviceName":"eth0","staticAddressConfigurations":[{"address":"192.168.0.90","prefixLength":24}],"staticDefaultRouter":"192.168.0.1"}`
	if string(got) != want {
		t.Fatalf("params:\n got %s\nwant %s", got, want)
	}

	// A wrong password is an authentication error and must not fall back to https.
	bad := NewClient(host, "root", "wrong", 5*time.Second)
	if _, err := bad.GetNetworkInfo(); err == nil || !strings.Contains(err.Error(), "authentication failed") || strings.Contains(err.Error(), "https") {
		t.Fatalf("err = %v", err)
	}
}

func TestClientFallbackHTTPS(t *testing.T) {
	cam := &fakeCamera{user: "root", pass: "secret"}
	srv := httptest.NewTLSServer(cam)
	defer srv.Close()
	// A TLS server answers plain http with 400, which is not treated as a redirect,
	// so pin the scheme to https and check that the self-signed certificate is accepted.
	c := NewClient(strings.TrimPrefix(srv.URL, "https://"), "root", "secret", 5*time.Second)
	c.scheme = "https"
	if _, err := c.GetNetworkInfo(); err != nil {
		t.Fatal(err)
	}
}
