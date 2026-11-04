// Package cloudflare is a small client for the Cloudflare API plus the
// persisted tunnel configuration phleet needs.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const DefaultBase = "https://api.cloudflare.com/client/v4"

type Client struct {
	Token string
	Base  string
	HTTP  *http.Client
}

func New(token string) *Client {
	return &Client{
		Token: strings.TrimSpace(token),
		Base:  DefaultBase,
		HTTP:  &http.Client{Timeout: 30 * time.Second},
	}
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type envelope struct {
	Success bool            `json:"success"`
	Errors  []apiError      `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "phleet-cli")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("cloudflare: HTTP %d: %s", resp.StatusCode, truncate(string(data), 300))
	}
	if !env.Success {
		msgs := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			msgs = append(msgs, fmt.Sprintf("%d: %s", e.Code, e.Message))
		}
		if len(msgs) == 0 {
			msgs = append(msgs, fmt.Sprintf("HTTP %d", resp.StatusCode))
		}
		return fmt.Errorf("cloudflare: %s", strings.Join(msgs, "; "))
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

type Zone struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	NameServers []string `json:"name_servers"`
	Account     struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"account"`
}

type Tunnel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

type Ingress struct {
	Hostname string `json:"hostname,omitempty"`
	Service  string `json:"service"`
	Path     string `json:"path,omitempty"`
}

type DNSRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

func (c *Client) Verify(ctx context.Context) error {
	var out struct {
		Status string `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, "/user/tokens/verify", nil, &out); err != nil {
		return err
	}
	if out.Status != "active" {
		return fmt.Errorf("token status is %q, expected active", out.Status)
	}
	return nil
}

func (c *Client) Zone(ctx context.Context, domain string) (Zone, error) {
	var zones []Zone
	path := "/zones?name=" + domain + "&per_page=50"
	if err := c.do(ctx, http.MethodGet, path, nil, &zones); err != nil {
		return Zone{}, err
	}
	for _, z := range zones {
		if strings.EqualFold(z.Name, domain) {
			return z, nil
		}
	}
	return Zone{}, fmt.Errorf("zone %q not found in this Cloudflare account (add the domain first)", domain)
}

func (c *Client) Tunnels(ctx context.Context, accountID, name string) ([]Tunnel, error) {
	var out []Tunnel
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel?name=%s&is_deleted=false", accountID, name)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) CreateTunnel(ctx context.Context, accountID, name string) (Tunnel, error) {
	var t Tunnel
	body := map[string]any{"name": name, "config_src": "cloudflare"}
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/accounts/%s/cfd_tunnel", accountID), body, &t)
	return t, err
}

func (c *Client) Tunnel(ctx context.Context, accountID, tunnelID string) (Tunnel, error) {
	var t Tunnel
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/accounts/%s/cfd_tunnel/%s", accountID, tunnelID), nil, &t)
	return t, err
}

func (c *Client) TunnelToken(ctx context.Context, accountID, tunnelID string) (string, error) {
	var tok string
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/token", accountID, tunnelID), nil, &tok)
	return tok, err
}

func (c *Client) PutConfiguration(ctx context.Context, accountID, tunnelID string, ingress []Ingress) error {
	body := map[string]any{"config": map[string]any{"ingress": ingress}}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/configurations", accountID, tunnelID), body, nil)
}

func (c *Client) DNSRecords(ctx context.Context, zoneID, name string) ([]DNSRecord, error) {
	var out []DNSRecord
	path := fmt.Sprintf("/zones/%s/dns_records?name=%s", zoneID, name)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpsertCNAME creates or updates a proxied CNAME record. It only modifies an
// existing CNAME; an A/AAAA (or other) record at the same name is left alone
// and reported as an error, because replacing it would silently break another
// service.
func (c *Client) UpsertCNAME(ctx context.Context, zoneID, name, target string) (string, error) {
	records, err := c.DNSRecords(ctx, zoneID, name)
	if err != nil {
		return "", err
	}
	body := map[string]any{
		"type":    "CNAME",
		"name":    name,
		"content": target,
		"proxied": true,
		"ttl":     1,
		"comment": "managed by phleet",
	}
	var cname *DNSRecord
	for i := range records {
		if strings.EqualFold(records[i].Type, "CNAME") {
			cname = &records[i]
			break
		}
	}
	if cname == nil {
		if len(records) > 0 {
			return "", fmt.Errorf("cannot create CNAME %s: an existing %s record uses that name", name, records[0].Type)
		}
		if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/zones/%s/dns_records", zoneID), body, nil); err != nil {
			return "", err
		}
		return "created", nil
	}
	if strings.EqualFold(cname.Content, target) && cname.Proxied {
		return "unchanged", nil
	}
	if err := c.do(ctx, http.MethodPatch, fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, cname.ID), body, nil); err != nil {
		return "", err
	}
	return "updated", nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

type Config struct {
	Domain      string `json:"domain"`
	ZoneID      string `json:"zoneId"`
	AccountID   string `json:"accountId"`
	TunnelID    string `json:"tunnelId"`
	TunnelName  string `json:"tunnelName"`
	APIToken    string `json:"apiToken,omitempty"`
	TunnelToken string `json:"tunnelToken,omitempty"`
	LocalURL    string `json:"localUrl"`
	UpdatedAt   int64  `json:"updatedAt"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

func SaveConfig(path string, c Config) error {
	c.UpdatedAt = time.Now().Unix()
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
