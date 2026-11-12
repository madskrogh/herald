package tenantapi

import (
	"strings"
	"testing"
)

func TestCheckEndpointURL(t *testing.T) {
	t.Parallel()

	// 2048 bytes in all.
	longest := "https://example.com/" + strings.Repeat("a", 2048-len("https://example.com/"))

	tests := []struct {
		name  string // the URL if empty
		url   string
		valid bool
	}{
		{url: "https://example.com/webhooks", valid: true},
		{url: "http://localhost:8080/webhooks", valid: true},
		{url: "https://203.0.113.7/hook?tenant=acme&v=2", valid: true},
		{url: "https://[2001:db8::1]:8443/", valid: true},
		{url: "HTTPS://Example.com/webhooks", valid: true},
		{name: "2048 bytes", url: longest, valid: true},
		{name: "2049 bytes", url: longest + "a"},
		{url: ""},
		{url: "example.com/webhooks"},
		{url: "//example.com/webhooks"},
		{url: "/webhooks"},
		{url: "ftp://example.com/webhooks"},
		{url: "https:example.com"},
		{url: "https:///webhooks"},
		{url: "https://:443/webhooks"},
		{url: "https://user:password@example.com/webhooks"},
		{url: "https://user@example.com/webhooks"},
		{url: "https://example.com/webhooks#section"},
		{url: "https://example.com/webhooks#"},
		{url: "https://example.com/web hooks"},
		{url: "https://exa mple.com/"},
	}
	for _, tt := range tests {
		name := tt.name
		if name == "" {
			name = tt.url
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := checkEndpointURL(tt.url)
			if tt.valid && err != nil {
				t.Errorf("checkEndpointURL = %v, want valid", err)
			}
			if !tt.valid && err == nil {
				t.Error("checkEndpointURL accepted it, want an error")
			}
		})
	}
}
