package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HostingRestoreTarget struct {
	ZoneID, RecordID, RecordName, RecordType, LiveContent string
	Proxied                                               bool
}
type HostingRestoreProvider interface {
	Restore(context.Context, HostingRestoreTarget) error
}
type HostingSuspendTarget struct {
	ZoneID, RecordID, RecordName, RecordType, MaintenanceContent string
	Proxied                                                      bool
}
type HostingSuspendProvider interface {
	Suspend(context.Context, HostingSuspendTarget) error
}
type cloudflareProvider struct {
	baseURL, token string
	timeout        time.Duration
	client         *http.Client
}

// NewCloudflareRestoreProvider validates the provider boundary. HTTP is allowed only for
// a loopback fake server, so local integration tests never need live credentials.
func NewCloudflareRestoreProvider(baseURL, token string, timeout time.Duration, client *http.Client) (*cloudflareProvider, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback())) || strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") || len(token) > 4096 || timeout <= 0 || timeout > time.Minute {
		return nil, errors.New("invalid Cloudflare configuration")
	}
	if client == nil {
		client = &http.Client{}
	}
	// Disable redirects to avoid disclosing the bearer token to a redirected host.
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &cloudflareProvider{strings.TrimRight(baseURL, "/"), token, timeout, &c}, nil
}

func (p *cloudflareProvider) Restore(ctx context.Context, t HostingRestoreTarget) error {
	return p.update(ctx, t.ZoneID, t.RecordID, t.RecordName, t.RecordType, t.LiveContent, t.Proxied)
}

func (p *cloudflareProvider) Suspend(ctx context.Context, t HostingSuspendTarget) error {
	return p.update(ctx, t.ZoneID, t.RecordID, t.RecordName, t.RecordType, t.MaintenanceContent, t.Proxied)
}

func (p *cloudflareProvider) update(ctx context.Context, zoneID, recordID, recordName, recordType, content string, proxied bool) error {
	if zoneID == "" || recordID == "" || len(zoneID) > 100 || len(recordID) > 100 || strings.ContainsAny(zoneID+recordID, "/\\?#") || recordName == "" || len(recordName) > 255 || recordType == "" || len(recordType) > 20 || content == "" || len(content) > 8192 {
		return errors.New("invalid Cloudflare target")
	}
	body, err := json.Marshal(map[string]any{"type": recordType, "name": recordName, "content": content, "proxied": proxied})
	if err != nil {
		return errors.New("invalid Cloudflare target")
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, p.baseURL+"/zones/"+url.PathEscape(zoneID)+"/dns_records/"+url.PathEscape(recordID), bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid Cloudflare request")
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("Cloudflare request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("Cloudflare request failed")
	}
	var result struct {
		Success bool `json:"success"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 65537)).Decode(&result) != nil || !result.Success {
		return errors.New("Cloudflare request failed")
	}
	return nil
}
