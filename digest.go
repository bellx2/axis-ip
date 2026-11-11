package main

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"
)

// digestChallenge holds a WWW-Authenticate: Digest challenge.
type digestChallenge struct {
	realm, nonce, opaque, algorithm, qop string
}

// parseChallenges extracts Digest challenges from WWW-Authenticate headers.
func parseChallenges(headers []string) []digestChallenge {
	var out []digestChallenge
	for _, h := range headers {
		if len(h) < 7 || !strings.EqualFold(h[:7], "Digest ") {
			continue
		}
		p := parseAuthParams(h[7:])
		out = append(out, digestChallenge{
			realm: p["realm"], nonce: p["nonce"], opaque: p["opaque"],
			algorithm: p["algorithm"], qop: p["qop"],
		})
	}
	return out
}

// pickChallenge prefers SHA-256 and falls back to MD5.
func pickChallenge(cs []digestChallenge) (digestChallenge, bool) {
	var md digestChallenge
	found := false
	for _, c := range cs {
		switch strings.ToUpper(c.algorithm) {
		case "SHA-256":
			return c, true
		case "", "MD5":
			if !found {
				md, found = c, true
			}
		}
	}
	return md, found
}

// parseAuthParams parses a list of key=value and key="value" pairs.
func parseAuthParams(s string) map[string]string {
	m := map[string]string{}
	for len(s) > 0 {
		s = strings.TrimLeft(s, " ,\t")
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			break
		}
		key := strings.ToLower(strings.TrimSpace(s[:eq]))
		s = s[eq+1:]
		var val string
		if strings.HasPrefix(s, `"`) {
			var b strings.Builder
			i := 1
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				b.WriteByte(s[i])
			}
			val = b.String()
			s = s[min(i+1, len(s)):]
		} else {
			end := strings.IndexByte(s, ',')
			if end < 0 {
				end = len(s)
			}
			val = strings.TrimSpace(s[:end])
			s = s[end:]
		}
		m[key] = val
	}
	return m
}

// digestAuthorization builds an Authorization header value (qop=auth or no qop).
func digestAuthorization(c digestChallenge, user, pass, method, uri string, nc int, cnonce string) (string, error) {
	var newHash func() hash.Hash
	alg := strings.ToUpper(c.algorithm)
	switch alg {
	case "", "MD5":
		newHash = md5.New
	case "SHA-256":
		newHash = sha256.New
	default:
		return "", fmt.Errorf("unsupported Digest algorithm: %s", c.algorithm)
	}
	h := func(s string) string {
		x := newHash()
		x.Write([]byte(s))
		return hex.EncodeToString(x.Sum(nil))
	}
	ha1 := h(user + ":" + c.realm + ":" + pass)
	ha2 := h(method + ":" + uri)

	useQop := false
	for _, q := range strings.Split(c.qop, ",") {
		if strings.TrimSpace(q) == "auth" {
			useQop = true
		}
	}
	if c.qop != "" && !useQop {
		return "", fmt.Errorf("unsupported qop: %s", c.qop)
	}

	var b strings.Builder
	fmt.Fprintf(&b, `Digest username="%s", realm="%s", nonce="%s", uri="%s"`, user, c.realm, c.nonce, uri)
	if useQop {
		ncs := fmt.Sprintf("%08x", nc)
		resp := h(ha1 + ":" + c.nonce + ":" + ncs + ":" + cnonce + ":auth:" + ha2)
		fmt.Fprintf(&b, `, qop=auth, nc=%s, cnonce="%s", response="%s"`, ncs, cnonce, resp)
	} else {
		fmt.Fprintf(&b, `, response="%s"`, h(ha1+":"+c.nonce+":"+ha2))
	}
	if c.algorithm != "" {
		fmt.Fprintf(&b, ", algorithm=%s", c.algorithm)
	}
	if c.opaque != "" {
		fmt.Fprintf(&b, `, opaque="%s"`, c.opaque)
	}
	return b.String(), nil
}

func newCnonce() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
