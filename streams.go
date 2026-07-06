package ce

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// scanSSE reads Server-Sent-Event frames from r, calling emit with each event's concatenated
// `data:` payload. emit returns false to stop early. Returns the read error (nil on clean EOF).
// Shared by the Tier-A message stream and every typed Tier-B stream so there is one SSE parser.
func scanSSE(ctx context.Context, r io.Reader, emit func([]byte) bool) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // allow large frames
	var data []string
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" { // event boundary
			if len(data) > 0 {
				if !emit([]byte(strings.Join(data, "\n"))) {
					return nil
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
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return sc.Err()
}

// sseOnce opens one SSE connection to path and pumps each event's data payload to emit until the
// stream drops or ctx ends. ctx governs the connection's lifetime (no client-level timeout).
func (c *Client) sseOnce(ctx context.Context, path string, emit func([]byte) bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	stream := &http.Client{Transport: c.HTTP.Transport}
	resp, err := stream.Do(req)
	if err != nil {
		return &Error{Method: "GET", Path: path, Detail: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return &Error{Method: "GET", Path: path, Status: resp.StatusCode, Detail: truncate(string(raw), 300)}
	}
	return scanSSE(ctx, resp.Body, emit)
}

// sseEvents returns a channel of raw event-data payloads from path, reconnecting with backoff on
// drops. The channel closes when ctx is cancelled.
func (c *Client) sseEvents(ctx context.Context, path string) <-chan []byte {
	out := make(chan []byte)
	go func() {
		defer close(out)
		backoff := 500 * time.Millisecond
		for ctx.Err() == nil {
			err := c.sseOnce(ctx, path, func(b []byte) bool {
				cp := append([]byte(nil), b...) // event data is reused by the scanner; copy it
				select {
				case out <- cp:
					return true
				case <-ctx.Done():
					return false
				}
			})
			if err != nil && ctx.Err() == nil {
				fmt.Fprintf(os.Stderr, "ce stream %s reconnecting: %v\n", path, err)
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
	return out
}

// sseTyped decodes each event of a stream into T, dropping frames that fail to parse.
func sseTyped[T any](c *Client, ctx context.Context, path string) <-chan T {
	raw := c.sseEvents(ctx, path)
	out := make(chan T)
	go func() {
		defer close(out)
		for b := range raw {
			var v T
			if json.Unmarshal(b, &v) != nil {
				continue
			}
			select {
			case out <- v:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// The chain event streams (block stream, transaction stream, and their BlockEvent/TxEvent types —
// TxEvent carries an Amount) are NOT substrate: the chain and its money are the economy ceapp's.
// That surface moved to the economy adapter's Go SDK (EconomyClient.Blocks / EconomyClient.
// Transactions), which rides this client's SSE transport hatch (Client.SSE). The core substrate SDK
// keeps only the CEP-1 signal stream below (cell signaling is a substrate protocol).

// Signal is one CEP-1 signal (from /signals and /signals/stream).
type Signal struct {
	From         string   `json:"from"`
	To           string   `json:"to"`
	Capabilities []string `json:"capabilities"`
	PayloadHex   string   `json:"payload_hex"`
	BurnProof    string   `json:"burn_proof"`
	Nonce        uint64   `json:"nonce"`
	ID           string   `json:"id"`
}

// SignalStream streams CEP-1 signals (SSE).
func (c *Client) SignalStream(ctx context.Context) <-chan Signal {
	return sseTyped[Signal](c, ctx, "/signals/stream")
}

// Signals returns the last ~100 CEP-1 signals (GET /signals snapshot).
func (c *Client) Signals(ctx context.Context) ([]Signal, error) {
	raw, err := c.do(ctx, http.MethodGet, "/signals", nil)
	if err != nil {
		return nil, err
	}
	var s []Signal
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return s, nil
}
