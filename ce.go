// Package ce is the Go client for a local CE node — a dependency-free (stdlib-only)
// client that lets a Go ceapp talk to its node exactly like the Rust (ce-rs), TypeScript
// (@ce-net/sdk) and Python (ce.py) clients do: publish/subscribe telemetry, directed
// request/reply, and a serve loop — all over the node's local HTTP API. No modules to
// pull, no build step beyond `go build`: `import "github.com/ce-net/ce-go"` and you are
// on the mesh.
//
//	c := ce.Connect()
//	c.Publish(ctx, "building/temp", []byte("21.5")) // fan out to every subscriber
//	msgs, _ := c.Subscribe(ctx, "building/temp")     // stream readings from the mesh
//	for m := range msgs {
//		fmt.Println(m.Sender, m.Text())
//	}
//
// Auth and endpoint mirror the other SDKs: `Authorization: Bearer <api-token>`; the token
// is read from $CE_API_TOKEN, else the node's `api.token` in the CE data dir. The node URL
// is $CE_NODE_URL (default http://127.0.0.1:8844). Payloads are []byte on the wire
// (hex-encoded in the JSON body, handled here); string helpers are provided for convenience.
//
// This is the app / mesh tier (Tier A) — the surface a ceapp actually reaches for. The full
// node tier (jobs, signals, streams, wallet, blobs, economy) grows on top of these same
// primitives, driven by the conformance kit. See PLAN/ce-polyglot-sdks.md.
package ce

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DefaultBaseURL is the local node's HTTP API. Override with $CE_NODE_URL or WithBaseURL.
const DefaultBaseURL = "http://127.0.0.1:8844"

// Error is a CE node API error: a non-2xx response, or an unreachable node.
type Error struct {
	Method string
	Path   string
	Status int    // 0 when the node was unreachable
	Detail string // node body (truncated) or transport reason
}

func (e *Error) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("%s %s -> node unreachable: %s", e.Method, e.Path, e.Detail)
	}
	return fmt.Sprintf("%s %s -> %d: %s", e.Method, e.Path, e.Status, e.Detail)
}

// Message is one inbound mesh message: a published telemetry item or a directed request.
type Message struct {
	Sender     string  // authenticated NodeId of the origin
	Topic      string  // the topic it arrived on
	Payload    []byte  // raw bytes (decoded from the wire's hex)
	ReplyToken *uint64 // set when the sender wants a reply (a request, not a publish)
}

// Text decodes the payload as UTF-8, for text telemetry.
func (m Message) Text() string { return string(m.Payload) }

// JSON unmarshals the payload into v.
func (m Message) JSON(v any) error { return json.Unmarshal(m.Payload, v) }

// WantsReply reports whether the sender is awaiting a Reply (i.e. it was a request).
func (m Message) WantsReply() bool { return m.ReplyToken != nil }

// Status is the subset of GET /status a mesh ceapp needs. Extra fields are ignored, so the
// client stays forward-compatible with newer nodes.
type Status struct {
	NodeID  string `json:"node_id"`
	Height  uint64 `json:"height"`
	Economy *bool  `json:"economy"` // nil or true = economy on; false = personal-mesh (--no-economy)
}

// Handler answers an inbound request. Return the reply payload for a request, or a nil
// payload to send no reply (e.g. for a fire-and-forget publish on the same topic). A
// returned error is logged and no reply is sent — a handler bug must not kill the responder.
type Handler func(Message) ([]byte, error)

// Client is a connection to the local CE node. Cheap to construct; holds no socket until used.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// Option configures a Client in Connect.
type Option func(*Client)

// WithBaseURL sets the node URL (default $CE_NODE_URL or DefaultBaseURL).
func WithBaseURL(u string) Option { return func(c *Client) { c.BaseURL = strings.TrimRight(u, "/") } }

// WithToken sets the bearer api-token explicitly (default: discovered from env / data dir).
func WithToken(t string) Option { return func(c *Client) { c.Token = t } }

// WithHTTPClient injects a custom *http.Client (timeouts, proxies, test doubles).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.HTTP = h } }

// Connect opens a client to the local CE node. The one call a ceapp starts with.
func Connect(opts ...Option) *Client {
	base := os.Getenv("CE_NODE_URL")
	if base == "" {
		base = DefaultBaseURL
	}
	c := &Client{
		BaseURL: strings.TrimRight(base, "/"),
		Token:   discoverToken(),
		HTTP:    &http.Client{Timeout: 35 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// dataDir resolves the CE data dir the same way the `ce` binary does per-OS.
func dataDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "ce")
	case "windows":
		if base := os.Getenv("APPDATA"); base != "" {
			return filepath.Join(base, "ce")
		}
		return filepath.Join(home, ".ce")
	default:
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "ce")
		}
		return filepath.Join(home, ".local", "share", "ce")
	}
}

func discoverToken() string {
	if t := strings.TrimSpace(os.Getenv("CE_API_TOKEN")); t != "" {
		return t
	}
	b, err := os.ReadFile(filepath.Join(dataDir(), "api.token"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ---- low-level HTTP ----

// do issues a JSON request and returns the raw response body. body may be nil (no request body).
func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// The node gates non-GET calls on Bearer auth; sending it on GETs too is harmless.
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, &Error{Method: method, Path: path, Detail: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &Error{Method: method, Path: path, Status: resp.StatusCode, Detail: truncate(string(raw), 300)}
	}
	return raw, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}


// ---- node ----

// Status returns GET /status — node id, height, economy flag. Also the liveness check.
func (c *Client) Status(ctx context.Context) (*Status, error) {
	raw, err := c.do(ctx, http.MethodGet, "/status", nil)
	if err != nil {
		return nil, err
	}
	var s Status
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// NodeID is the local node's id (from /status).
func (c *Client) NodeID(ctx context.Context) (string, error) {
	s, err := c.Status(ctx)
	if err != nil {
		return "", err
	}
	return s.NodeID, nil
}

// WaitReady blocks until the node answers /status, so a daemon started at boot doesn't race it.
func (c *Client) WaitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := c.Status(ctx); err == nil {
			return nil
		} else if time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// ---- pub/sub (fan-out telemetry) ----

// Publish signs and fans payload out to every subscriber of topic (POST /mesh/publish).
func (c *Client) Publish(ctx context.Context, topic string, payload []byte) error {
	_, err := c.do(ctx, http.MethodPost, "/mesh/publish", map[string]any{
		"topic": topic, "payload_hex": hex.EncodeToString(payload),
	})
	return err
}

// SubscribeTopic registers interest in topic (POST /mesh/subscribe). Idempotent; lasts the
// node's lifetime. Subscribe/Serve call this for you.
func (c *Client) SubscribeTopic(ctx context.Context, topic string) error {
	_, err := c.do(ctx, http.MethodPost, "/mesh/subscribe", map[string]any{"topic": topic})
	return err
}

// Subscribe subscribes to topics and returns a channel of matching inbound Messages. The
// channel closes when ctx is cancelled. Best-effort fan-out (a dropped item is superseded by
// the next); use Serve for request/reply. The stream reconnects automatically on transient drops.
func (c *Client) Subscribe(ctx context.Context, topics ...string) (<-chan Message, error) {
	want := map[string]bool{}
	for _, t := range topics {
		want[t] = true
	}
	all, err := c.Messages(ctx, topics...)
	if err != nil {
		return nil, err
	}
	if len(want) == 0 {
		return all, nil
	}
	out := make(chan Message)
	go func() {
		defer close(out)
		for m := range all {
			if want[m.Topic] {
				select {
				case out <- m:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// ---- directed request/reply (reliable) ----

// Send delivers a one-way directed message to node `to` (POST /mesh/send).
func (c *Client) Send(ctx context.Context, to, topic string, payload []byte) error {
	_, err := c.do(ctx, http.MethodPost, "/mesh/send", map[string]any{
		"to": to, "topic": topic, "payload_hex": hex.EncodeToString(payload),
	})
	return err
}

// Request makes a reliable request to node `to` and returns the reply payload
// (POST /mesh/request). timeout bounds the wait; the HTTP call is given a little headroom on top.
func (c *Client) Request(ctx context.Context, to, topic string, payload []byte, timeout time.Duration) ([]byte, error) {
	rctx, cancel := context.WithTimeout(ctx, timeout+5*time.Second)
	defer cancel()
	raw, err := c.do(rctx, http.MethodPost, "/mesh/request", map[string]any{
		"to": to, "topic": topic, "payload_hex": hex.EncodeToString(payload),
		"timeout_ms": timeout.Milliseconds(),
	})
	if err != nil {
		return nil, err
	}
	var r struct {
		PayloadHex string `json:"payload_hex"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return hex.DecodeString(r.PayloadHex)
}

// Reply answers an inbound request by its ReplyToken (POST /mesh/reply).
func (c *Client) Reply(ctx context.Context, token uint64, payload []byte) error {
	_, err := c.do(ctx, http.MethodPost, "/mesh/reply", map[string]any{
		"token": token, "payload_hex": hex.EncodeToString(payload),
	})
	return err
}

// ---- serve loop (be a mesh responder / capability provider) ----

// Serve subscribes to topics and answers every inbound request with handler. It blocks until
// ctx is cancelled (then returns ctx.Err()) or the stream cannot be re-opened. A handler that
// returns an error is logged to stderr and skipped — it never kills the responder.
func (c *Client) Serve(ctx context.Context, topics []string, handler Handler) error {
	subs := map[string]bool{}
	for _, t := range topics {
		subs[t] = true
	}
	msgs, err := c.Messages(ctx, topics...)
	if err != nil {
		return err
	}
	for m := range msgs {
		if len(subs) > 0 && !subs[m.Topic] {
			continue
		}
		out, herr := handler(m)
		if herr != nil {
			fmt.Fprintf(os.Stderr, "ce.Serve handler error on %s: %v\n", m.Topic, herr)
			continue
		}
		if out != nil && m.ReplyToken != nil {
			if err := c.Reply(ctx, *m.ReplyToken, out); err != nil {
				fmt.Fprintf(os.Stderr, "ce.Serve reply error on %s: %v\n", m.Topic, err)
			}
		}
	}
	return ctx.Err()
}

// ---- inbound SSE stream ----

// Messages returns a channel of every inbound mesh message from GET /mesh/messages/stream
// (Server-Sent Events), reconnecting with backoff on drops. It optionally subscribes to the
// given topics first so they start arriving. The channel closes when ctx is cancelled.
func (c *Client) Messages(ctx context.Context, subscribe ...string) (<-chan Message, error) {
	for _, t := range subscribe {
		if err := c.SubscribeTopic(ctx, t); err != nil {
			return nil, err
		}
	}
	out := make(chan Message)
	go func() {
		defer close(out)
		backoff := 500 * time.Millisecond
		for ctx.Err() == nil {
			if err := c.streamOnce(ctx, out); err != nil && ctx.Err() == nil {
				fmt.Fprintf(os.Stderr, "ce stream reconnecting: %v\n", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				if backoff *= 2; backoff > 10*time.Second {
					backoff = 10 * time.Second
				}
				continue
			}
			backoff = 500 * time.Millisecond
		}
	}()
	return out, nil
}

// streamOnce opens one SSE connection and pumps parsed messages to out until it drops or ctx ends.
func (c *Client) streamOnce(ctx context.Context, out chan<- Message) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/mesh/messages/stream", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	// No client timeout on the stream itself; ctx governs its lifetime.
	streamHTTP := &http.Client{Transport: c.HTTP.Transport}
	resp, err := streamHTTP.Do(req)
	if err != nil {
		return &Error{Method: "GET", Path: "/mesh/messages/stream", Detail: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return &Error{Method: "GET", Path: "/mesh/messages/stream", Status: resp.StatusCode, Detail: truncate(string(raw), 300)}
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // allow large frames
	var data []string
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" { // event boundary
			if len(data) > 0 {
				if m, ok := parseMessage(strings.Join(data, "\n")); ok {
					select {
					case out <- m:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				data = data[:0]
			}
			continue
		}
		if strings.HasPrefix(line, ":") { // SSE comment / keep-alive
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	return sc.Err()
}

func parseMessage(data string) (Message, bool) {
	var r struct {
		From       string  `json:"from"`
		Topic      string  `json:"topic"`
		PayloadHex string  `json:"payload_hex"`
		ReplyToken *uint64 `json:"reply_token"`
	}
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		return Message{}, false
	}
	payload, _ := hex.DecodeString(r.PayloadHex)
	return Message{Sender: r.From, Topic: r.Topic, Payload: payload, ReplyToken: r.ReplyToken}, true
}
