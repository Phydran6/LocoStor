package hostagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Phydran6/LocoStor/internal/tasks"
)

// Client talks to the agent from inside the container (for background
// jobs; the browser goes through Proxy).
type Client struct {
	socket string
	hc     *http.Client
	// Handler replaces the socket (demo mode).
	Handler http.Handler
}

// NewClient creates a client for the agent socket.
func NewClient(socket string) *Client {
	return &Client{socket: socket, hc: &http.Client{
		Timeout: 2 * time.Minute,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		}},
	}}
}

// Available reports whether the agent socket exists.
func (c *Client) Available() bool {
	if c == nil {
		return false
	}
	if c.Handler != nil {
		return true
	}
	_, err := os.Stat(c.socket)
	return err == nil
}

// Do sends a JSON request and decodes the JSON answer into out.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://locostor-host"+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	var resp *http.Response
	if c.Handler != nil {
		rec := &recorder{header: http.Header{}, code: 200}
		c.Handler.ServeHTTP(rec, req)
		resp = &http.Response{StatusCode: rec.code, Body: io.NopCloser(&rec.body)}
	} else if resp, err = c.hc.Do(req); err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return fmt.Errorf("host: %s", e.Error)
		}
		return fmt.Errorf("host: %s %s: HTTP %d", method, path, resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Info returns the agent info.
func (c *Client) Info(ctx context.Context) (Info, error) {
	var i Info
	err := c.Do(ctx, http.MethodGet, "/info", nil, &i)
	return i, err
}

// HasFeature reports whether the agent supports f.
func (i Info) HasFeature(f string) bool {
	for _, x := range i.Features {
		if x == f {
			return true
		}
	}
	return false
}

// Follow mirrors the log of an agent task into t until it ends.
func (c *Client) Follow(t *tasks.Task, id, prefix string) (tasks.Info, error) {
	from := 0
	misses := 0
	for {
		var r struct {
			Task  tasks.Info `json:"task"`
			Lines []string   `json:"lines"`
			Next  int        `json:"next"`
		}
		err := c.Do(t.Context(), http.MethodGet, fmt.Sprintf("/tasks/%s?from=%d", id, from), nil, &r)
		if err != nil {
			if misses++; misses > 30 {
				return tasks.Info{}, err
			}
		} else {
			misses = 0
			for _, l := range r.Lines {
				t.Logf("%s%s", prefix, l)
			}
			from = r.Next
			if r.Task.Status != tasks.Running {
				return r.Task, nil
			}
		}
		select {
		case <-t.Context().Done():
			return tasks.Info{}, t.Context().Err()
		case <-time.After(700 * time.Millisecond):
		}
	}
}

// UpdateAgent updates the agent binary to the latest release and waits
// until the new version answers.
func (c *Client) UpdateAgent(t *tasks.Task) error {
	before, err := c.Info(t.Context())
	if err != nil {
		return err
	}
	if err := c.Do(t.Context(), http.MethodPost, "/update", nil, nil); err != nil {
		if strings.Contains(err.Error(), "up to date") {
			t.Logf("The host agent is up to date (%s).", before.Version)
			return nil
		}
		return err
	}
	t.Logf("Host agent %s is updating…", before.Version)
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(1500 * time.Millisecond)
		if i, err := c.Info(t.Context()); err == nil && i.Version != before.Version {
			t.Logf("Host agent now runs version %s.", i.Version)
			return nil
		}
	}
	return fmt.Errorf("the host agent did not come back within 2 minutes")
}

type recorder struct {
	header http.Header
	body   bytes.Buffer
	code   int
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *recorder) WriteHeader(code int)        { r.code = code }
