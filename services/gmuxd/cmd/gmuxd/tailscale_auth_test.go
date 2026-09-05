package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTailscaleHTTPHandlerTokenPolicy(t *testing.T) {
	common := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	authed := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })

	for _, tt := range []struct {
		name         string
		requireToken bool
		want         int
	}{
		{name: "token required by default", requireToken: true, want: http.StatusUnauthorized},
		{name: "tailscale identity only", requireToken: false, want: http.StatusNoContent},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tailscaleHTTPHandler(tt.requireToken, common, authed).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
			if rr.Code != tt.want {
				t.Fatalf("status = %d, want %d", rr.Code, tt.want)
			}
		})
	}
}
