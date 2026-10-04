package hostagent

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"time"
)

// Proxy forwards requests (with the /api/host prefix already stripped) to
// the agent on socket. Authentication and CSRF checks happen in the
// container's API before a request gets here.
func Proxy(socket string) http.Handler {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: 2 * time.Minute,
	}
	rp := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme = "http"
			r.Out.URL.Host = "locostor-host"
			r.Out.Host = "locostor-host"
			// The agent needs nothing from the browser but the JSON body.
			for _, h := range []string{"Cookie", "Authorization", "Origin", "Referer", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
				r.Out.Header.Del(h)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			unavailable(w, r, "the Proxmox host agent is not responding: "+err.Error())
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(socket); err != nil {
			unavailable(w, r, "the Proxmox host is not connected")
			return
		}
		rp.ServeHTTP(w, r)
	})
}

// unavailable answers /info with available=false so the UI can show how to
// connect the host; everything else gets 503.
func unavailable(w http.ResponseWriter, r *http.Request, msg string) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/info" {
		_ = json.NewEncoder(w).Encode(map[string]any{"available": false, "error": msg})
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
