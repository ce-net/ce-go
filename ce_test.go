package ce

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeNode is a minimal stand-in for the CE node HTTP API, enough to exercise the Tier-A
// surface without a live node. It is the local seed of the language-agnostic conformance kit.
type fakeNode struct {
	mu          chan struct{}
	published   map[string][]byte // topic -> last payload
	sent        map[string][]byte // to+topic -> payload
	replies     chan replied
	lastInstall map[string]string // last /mesh-app-install request body
}

type replied struct {
	token   uint64
	payload []byte
}

func newFakeNode() *fakeNode {
	return &fakeNode{
		mu:          make(chan struct{}, 1),
		published:   map[string][]byte{},
		sent:        map[string][]byte{},
		replies:     make(chan replied, 4),
		lastInstall: map[string]string{},
	}
}

func (f *fakeNode) lock()   { f.mu <- struct{}{} }
func (f *fakeNode) unlock() { <-f.mu }

func (f *fakeNode) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"node_id":"abc123","peer_id":"p","listen_port":4001,"economy":true,` +
			`"height":42,"balance":"1000000000000000000","free":"1000000000000000000"}`))
	})
	mux.HandleFunc("/mesh/publish", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Topic      string `json:"topic"`
			PayloadHex string `json:"payload_hex"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		p, _ := hex.DecodeString(b.PayloadHex)
		f.lock()
		f.published[b.Topic] = p
		f.unlock()
		w.WriteHeader(200)
	})
	mux.HandleFunc("/mesh/send", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			To         string `json:"to"`
			Topic      string `json:"topic"`
			PayloadHex string `json:"payload_hex"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		p, _ := hex.DecodeString(b.PayloadHex)
		f.lock()
		f.sent[b.To+"|"+b.Topic] = p
		f.unlock()
		w.WriteHeader(200)
	})
	mux.HandleFunc("/mesh/subscribe", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/mesh/request", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			To         string `json:"to"`
			Topic      string `json:"topic"`
			PayloadHex string `json:"payload_hex"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		in, _ := hex.DecodeString(b.PayloadHex)
		out := append([]byte("reply:"), in...)
		json.NewEncoder(w).Encode(map[string]string{"payload_hex": hex.EncodeToString(out)})
	})
	mux.HandleFunc("/mesh/reply", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Token      uint64 `json:"token"`
			PayloadHex string `json:"payload_hex"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		p, _ := hex.DecodeString(b.PayloadHex)
		f.replies <- replied{token: b.Token, payload: p}
		w.WriteHeader(200)
	})
	mux.HandleFunc("/mesh-app-install", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			NodeID   string `json:"node_id"`
			App      string `json:"app"`
			Registry string `json:"registry"`
			Grant    string `json:"grant"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		f.lock()
		f.lastInstall = map[string]string{
			"node_id": b.NodeID, "app": b.App, "registry": b.Registry, "grant": b.Grant,
		}
		f.unlock()
		json.NewEncoder(w).Encode(map[string]string{"app": b.App, "version": "1.2.3"})
	})
	// SSE: emit exactly one inbound request frame, then hold the connection open on ctx.
	mux.HandleFunc("/mesh/messages/stream", func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flush", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		token := uint64(7)
		frame, _ := json.Marshal(map[string]any{
			"from": "peer9", "topic": "demo/echo",
			"payload_hex": hex.EncodeToString([]byte("ping")), "reply_token": token,
		})
		fmt.Fprintf(w, "data: %s\n\n", frame)
		fl.Flush()
		<-r.Context().Done()
	})
	return mux
}

func TestStatus(t *testing.T) {
	srv := httptest.NewServer(newFakeNode().handler())
	defer srv.Close()
	c := Connect(WithBaseURL(srv.URL), WithToken("t"))
	s, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.NodeID != "abc123" || s.PeerID != "p" || s.ListenPort != 4001 {
		t.Fatalf("bad substrate status: %+v", s)
	}
	if !s.EconomyEnabled() {
		t.Fatalf("expected economy enabled, got %v", s.Economy)
	}
}

func TestMeshAppInstall(t *testing.T) {
	f := newFakeNode()
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	c := Connect(WithBaseURL(srv.URL), WithToken("t"))

	got, err := c.MeshAppInstall(context.Background(), "abc123", "foo", "https://ce-net.com", "cap")
	if err != nil {
		t.Fatal(err)
	}
	if got.App != "foo" || got.Version != "1.2.3" {
		t.Fatalf("bad AppInstalled: %+v", got)
	}
	// the wire body mirrors `ce app install <app> --on node=<id>`
	f.lock()
	li := f.lastInstall
	f.unlock()
	want := map[string]string{"node_id": "abc123", "app": "foo", "registry": "https://ce-net.com", "grant": "cap"}
	for k, v := range want {
		if li[k] != v {
			t.Fatalf("install body[%s]=%q want %q (full: %+v)", k, li[k], v, li)
		}
	}
}

func TestPublishAndSendHexRoundTrip(t *testing.T) {
	f := newFakeNode()
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	c := Connect(WithBaseURL(srv.URL), WithToken("t"))
	ctx := context.Background()

	if err := c.Publish(ctx, "building/temp", []byte("21.5")); err != nil {
		t.Fatal(err)
	}
	if err := c.Send(ctx, "peer1", "cmd", []byte{0x00, 0xff, 0x10}); err != nil {
		t.Fatal(err)
	}
	f.lock()
	defer f.unlock()
	if got := string(f.published["building/temp"]); got != "21.5" {
		t.Fatalf("publish payload = %q", got)
	}
	if got := f.sent["peer1|cmd"]; len(got) != 3 || got[1] != 0xff {
		t.Fatalf("send binary payload not preserved: %v", got)
	}
}

func TestRequestDecodesReply(t *testing.T) {
	srv := httptest.NewServer(newFakeNode().handler())
	defer srv.Close()
	c := Connect(WithBaseURL(srv.URL), WithToken("t"))
	out, err := c.Request(context.Background(), "peer1", "echo", []byte("hi"), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "reply:hi" {
		t.Fatalf("request reply = %q", out)
	}
}

// TestServeRoundTrip proves the SSE parse -> handler -> reply loop end to end.
func TestServeRoundTrip(t *testing.T) {
	f := newFakeNode()
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	c := Connect(WithBaseURL(srv.URL), WithToken("t"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go c.Serve(ctx, []string{"demo/echo"}, func(m Message) ([]byte, error) {
		if m.Sender != "peer9" || m.Text() != "ping" || !m.WantsReply() {
			t.Errorf("unexpected inbound message: %+v", m)
		}
		return []byte("pong:" + m.Text()), nil
	})

	select {
	case r := <-f.replies:
		if r.token != 7 || string(r.payload) != "pong:ping" {
			t.Fatalf("bad reply: token=%d payload=%q", r.token, r.payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for serve to reply")
	}
}

func TestErrorSurfacesNodeDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(402)
		io.WriteString(w, "insufficient funds")
	}))
	defer srv.Close()
	c := Connect(WithBaseURL(srv.URL), WithToken("t"))
	_, err := c.Status(context.Background())
	ce, ok := err.(*Error)
	if !ok {
		t.Fatalf("want *Error, got %T", err)
	}
	if ce.Status != 402 || ce.Detail != "insufficient funds" {
		t.Fatalf("error did not carry node detail: %+v", ce)
	}
}

func TestCoreNodeSlimStatus(t *testing.T) {
	// A core (economy-free) node returns only node_id/peer_id/listen_port/economy.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"node_id":"abc123","peer_id":"12D3KooWCoreNode","listen_port":4001,"economy":false}`))
	}))
	defer srv.Close()
	c := Connect(WithBaseURL(srv.URL), WithToken("t"))
	s, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.NodeID != "abc123" || s.PeerID != "12D3KooWCoreNode" || s.ListenPort != 4001 {
		t.Fatalf("core status decode: %+v", s)
	}
	if s.EconomyEnabled() {
		t.Fatal("economy should be disabled on a core node")
	}
}
