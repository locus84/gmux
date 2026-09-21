package wsproxy

import (
	"net/url"
	"testing"
)

func TestBrowserAttachQueryAllowlist(t *testing.T) {
	cases := []struct {
		name              string
		query             string
		browser, imageRef bool
	}{
		{"browser", "client=browser", true, false},
		{"image refs", "client=browser&images=refs-v1", true, true},
		{"missing browser", "images=refs-v1", false, false},
		{"missing", "", false, false},
		{"other", "client=browser&route=secret", false, false},
		{"wrong image value", "client=browser&images=raw", false, false},
		{"duplicate image", "client=browser&images=refs-v1&images=refs-v1", false, false},
		{"wrong value", "client=1", false, false},
		{"duplicate", "client=browser&client=browser", false, false},
		{"empty duplicate", "client=browser&client=", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			browser, imageRef := browserAttachQuery(mustParseQuery(t, tc.query))
			if browser != tc.browser || imageRef != tc.imageRef {
				t.Fatalf("browserAttachQuery(%q) = (%v,%v), want (%v,%v)", tc.query, browser, imageRef, tc.browser, tc.imageRef)
			}
		})
	}
}

func mustParseQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	q, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	return q
}
