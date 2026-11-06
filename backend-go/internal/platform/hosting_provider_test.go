package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCloudflareRestoreUsesLiveValues(t *testing.T) {
	var got map[string]any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/zones/zone/dns_records/record" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer s.Close()
	p, err := NewCloudflareRestoreProvider(s.URL, "secret", time.Second, s.Client())
	if err != nil {
		t.Fatal(err)
	}
	err = p.Restore(context.Background(), HostingRestoreTarget{ZoneID: "zone", RecordID: "record", RecordName: "www.example.com", RecordType: "A", LiveContent: "192.0.2.1", Proxied: true})
	if err != nil {
		t.Fatal(err)
	}
	if got["content"] != "192.0.2.1" || got["name"] != "www.example.com" || got["type"] != "A" || got["proxied"] != true {
		t.Fatalf("unexpected payload: %#v", got)
	}
}

func TestCloudflareProviderRejectsUnsafeConfiguration(t *testing.T) {
	for _, tc := range []struct {
		base, token string
		timeout     time.Duration
	}{
		{"http://example.com", "secret", time.Second}, {"https://user:pass@example.com", "secret", time.Second}, {"https://example.com?secret=x", "secret", time.Second}, {"https://example.com", "", time.Second}, {"https://example.com", "bad\nsecret", time.Second}, {"https://example.com", "secret", 0}, {"https://example.com", "secret", time.Hour},
	} {
		if _, err := NewCloudflareRestoreProvider(tc.base, tc.token, tc.timeout, nil); err == nil {
			t.Errorf("accepted invalid configuration: %#v", tc)
		}
	}
}

func TestCloudflareProviderRedactsFailures(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusOK} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"secret"}]}`))
		}))
		p, err := NewCloudflareRestoreProvider(s.URL, "secret", time.Second, s.Client())
		if err != nil {
			t.Fatal(err)
		}
		err = p.Restore(context.Background(), HostingRestoreTarget{ZoneID: "z", RecordID: "r", RecordName: "x", RecordType: "A", LiveContent: "127.0.0.1"})
		s.Close()
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}

func TestCloudflareProviderRespectsCancellation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(100 * time.Millisecond) }))
	defer s.Close()
	p, err := NewCloudflareRestoreProvider(s.URL, "secret", 20*time.Millisecond, s.Client())
	if err != nil {
		t.Fatal(err)
	}
	err = p.Restore(context.Background(), HostingRestoreTarget{ZoneID: "z", RecordID: "r", RecordName: "x", RecordType: "A", LiveContent: "127.0.0.1"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestCloudflareProviderRejectsOversizedTarget(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected provider call") }))
	defer s.Close()
	p, err := NewCloudflareRestoreProvider(s.URL, "secret", time.Second, s.Client())
	if err != nil {
		t.Fatal(err)
	}
	err = p.Restore(context.Background(), HostingRestoreTarget{ZoneID: "z", RecordID: "r", RecordName: "name", RecordType: "A", LiveContent: strings.Repeat("x", 8193)})
	if err == nil {
		t.Fatal("oversized target accepted")
	}
}

func TestCloudflareSuspendUsesMaintenanceValues(t *testing.T) {
	var got map[string]any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/zones/zone/dns_records/record" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("unexpected request")
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer s.Close()
	p, err := NewCloudflareRestoreProvider(s.URL, "secret", time.Second, s.Client())
	if err != nil {
		t.Fatal(err)
	}
	err = p.Suspend(context.Background(), HostingSuspendTarget{ZoneID: "zone", RecordID: "record", RecordName: "www.example.com", RecordType: "A", MaintenanceContent: "192.0.2.2", Proxied: true})
	if err != nil {
		t.Fatal(err)
	}
	if got["content"] != "192.0.2.2" || got["name"] != "www.example.com" || got["type"] != "A" || got["proxied"] != true {
		t.Fatalf("unexpected payload: %#v", got)
	}
}
