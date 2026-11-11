package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const networkSettingsPath = "/axis-cgi/network_settings.cgi"

// Client is a VAPIX client for a single device.
type Client struct {
	Host   string // IP address or hostname
	User   string
	Pass   string
	scheme string // empty until known; switches to https when http is unreachable
	http   *http.Client
	https  *http.Client
}

func NewClient(host, user, pass string, timeout time.Duration) *Client {
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{
		Host: host, User: user, Pass: pass,
		http: &http.Client{Timeout: timeout, CheckRedirect: noRedirect},
		https: &http.Client{
			Timeout:       timeout,
			CheckRedirect: noRedirect,
			// Devices often use self-signed certificates, so skip verification (https only).
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		},
	}
}

// errSwitchScheme is an internal error that triggers a retry over https.
var errSwitchScheme = errors.New("switch scheme")

// post sends JSON with Digest authentication and returns the response body.
func (c *Client) post(path string, body []byte) ([]byte, error) {
	if c.scheme != "" {
		return c.postScheme(c.scheme, path, body)
	}
	b, err := c.postScheme("http", path, body)
	if err == nil {
		c.scheme = "http"
		return b, nil
	}
	// Try https only when http is unreachable or redirects.
	if !errors.Is(err, errSwitchScheme) {
		return nil, err
	}
	b, err2 := c.postScheme("https", path, body)
	if err2 != nil {
		return nil, fmt.Errorf("http: %v / https: %v", err, err2)
	}
	c.scheme = "https"
	return b, nil
}

func (c *Client) postScheme(scheme, path string, body []byte) ([]byte, error) {
	hc := c.http
	if scheme == "https" {
		hc = c.https
	}
	url := scheme + "://" + c.Host + path
	send := func(auth string) (*http.Response, error) {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		return hc.Do(req)
	}

	resp, err := send("")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errSwitchScheme, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		ch, ok := pickChallenge(parseChallenges(resp.Header.Values("WWW-Authenticate")))
		drain(resp)
		if !ok {
			return nil, fmt.Errorf("%s: no Digest authentication challenge", url)
		}
		auth, err := digestAuthorization(ch, c.User, c.Pass, http.MethodPost, path, 1, newCnonce())
		if err != nil {
			return nil, err
		}
		if resp, err = send(auth); err != nil {
			return nil, err
		}
	}
	defer drain(resp)
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, fmt.Errorf("%w: %s redirected (%d)", errSwitchScheme, url, resp.StatusCode)
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, fmt.Errorf("%s: authentication failed (wrong user name or password)", url)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%s: HTTP %d: %s", url, resp.StatusCode, truncate(data))
	}
	return data, nil
}

func drain(resp *http.Response) {
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

func truncate(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "..."
	}
	return string(b)
}

// call invokes a network_settings.cgi method and decodes its data into out.
func (c *Client) call(method string, params any, out any) error {
	req := map[string]any{"apiVersion": "1.0", "context": "axis-ip", "method": method}
	if params != nil {
		req["params"] = params
	}
	body, _ := json.Marshal(req)
	data, err := c.post(networkSettingsPath, body)
	if err != nil {
		return err
	}
	var resp struct {
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("cannot parse response: %v: %s", err, truncate(data))
	}
	if resp.Error != nil {
		return fmt.Errorf("%s failed (code %d): %s", method, resp.Error.Code, resp.Error.Message)
	}
	if out != nil && len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, out); err != nil {
			return fmt.Errorf("cannot parse data: %v: %s", err, truncate(data))
		}
	}
	return nil
}

// NetworkInfo is the part of the getNetworkInfo response that axis-ip uses.
type NetworkInfo struct {
	Devices []NetDevice `json:"devices"`
}

type NetDevice struct {
	Name       string `json:"name"`
	MACAddress string `json:"macAddress"`
	IPv4       struct {
		ConfigurationMode           string     `json:"configurationMode"`
		Addresses                   []AddrConf `json:"addresses"`
		DefaultRouter               string     `json:"defaultRouter"`
		StaticDefaultRouter         string     `json:"staticDefaultRouter"`
		StaticAddressConfigurations []AddrConf `json:"staticAddressConfigurations"`
	} `json:"IPv4"`
}

type AddrConf struct {
	Address      string `json:"address"`
	PrefixLength int    `json:"prefixLength"`
	Origin       string `json:"origin,omitempty"`
}

func (c *Client) GetNetworkInfo() (*NetworkInfo, error) {
	var info NetworkInfo
	if err := c.call("getNetworkInfo", map[string]any{}, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// IPv4Config is the params object of setIPv4AddressConfiguration.
type IPv4Config struct {
	DeviceName                  string     `json:"deviceName"`
	ConfigurationMode           string     `json:"configurationMode"`
	StaticDefaultRouter         string     `json:"staticDefaultRouter,omitempty"`
	StaticAddressConfigurations []AddrConf `json:"staticAddressConfigurations,omitempty"`
}

func (c *Client) SetIPv4(cfg IPv4Config) error {
	return c.call("setIPv4AddressConfiguration", cfg, nil)
}
