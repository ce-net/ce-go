package ce

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAmountParseAndRender(t *testing.T) {
	cases := []struct{ in, credits, base string }{
		{"1", "1", "1000000000000000000"},
		{"1.5", "1.5", "1500000000000000000"},
		{"0.000000000000000001", "0.000000000000000001", "1"},
		{"1000000000", "1000000000", "1000000000000000000000000000"}, // 1e9 credits > 2^53
		{".5", "0.5", "500000000000000000"},
		{"5.", "5", "5000000000000000000"},
	}
	for _, c := range cases {
		a, err := ParseCredits(c.in)
		if err != nil {
			t.Fatalf("ParseCredits(%q): %v", c.in, err)
		}
		if a.Credits() != c.credits {
			t.Errorf("ParseCredits(%q).Credits() = %q, want %q", c.in, a.Credits(), c.credits)
		}
		if a.Base().String() != c.base {
			t.Errorf("ParseCredits(%q).Base() = %s, want %s", c.in, a.Base(), c.base)
		}
	}
}

func TestAmountRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "  ", "-1", "1.2.3", "abc", "1.0000000000000000001" /* 19 dp */} {
		if _, err := ParseCredits(bad); err == nil {
			t.Errorf("ParseCredits(%q) should have failed", bad)
		}
	}
}

func TestAmountJSONIsBaseUnitString(t *testing.T) {
	a := FromCredits(2) // 2e18 base units
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"2000000000000000000"` {
		t.Fatalf("Amount JSON = %s, want quoted base-unit string", b)
	}
	// Round-trip a value far beyond 2^53 to prove big-int handling.
	var got Amount
	if err := json.Unmarshal([]byte(`"123456789012345678901234567890"`), &got); err != nil {
		t.Fatal(err)
	}
	if got.Base().String() != "123456789012345678901234567890" {
		t.Fatalf("big amount round-trip lost precision: %s", got.Base())
	}
	// A bare JSON number must also decode.
	var n Amount
	if err := json.Unmarshal([]byte(`42`), &n); err != nil || n.Base().Int64() != 42 {
		t.Fatalf("bare number amount decode: %v (%s)", err, n.Base())
	}
}

func TestCIDMatchesSHA256(t *testing.T) {
	data := []byte("hello")
	h := sha256.Sum256(data)
	if CID(data) != hex.EncodeToString(h[:]) {
		t.Fatalf("CID != sha256")
	}
}

// blobStore is a fake content-addressed node for object round-trip + jobs decode tests.
func blobFakeNode() (http.Handler, map[string][]byte) {
	store := map[string][]byte{}
	mux := http.NewServeMux()
	mux.HandleFunc("/blobs", func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 0)
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			body = append(body, buf[:n]...)
			if err != nil {
				break
			}
		}
		h := sha256.Sum256(body)
		hash := hex.EncodeToString(h[:])
		store[hash] = body
		json.NewEncoder(w).Encode(map[string]string{"hash": hash})
	})
	mux.HandleFunc("/blobs/", func(w http.ResponseWriter, r *http.Request) {
		hash := r.URL.Path[len("/blobs/"):]
		b, ok := store[hash]
		if !ok {
			http.Error(w, "not found", 404)
			return
		}
		w.Write(b)
	})
	return mux, store
}

func TestObjectRoundTripAndDedup(t *testing.T) {
	h, store := blobFakeNode()
	srv := httptest.NewServer(h)
	defer srv.Close()
	c := Connect(WithBaseURL(srv.URL), WithToken("t"))
	ctx := context.Background()

	// > 2 chunks so the manifest path is exercised.
	data := make([]byte, DefaultChunkSize*2+123)
	for i := range data {
		data[i] = byte(i * 7)
	}
	cid, err := c.PutObject(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.GetObject(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(data) {
		t.Fatalf("object length = %d, want %d", len(got), len(data))
	}
	for i := range data {
		if got[i] != data[i] {
			t.Fatalf("object byte %d differs", i)
		}
	}
	// The manifest + 3 chunks = 4 blobs; identical content re-put must not add blobs (dedup by CID).
	before := len(store)
	if _, err := c.PutObject(ctx, data); err != nil {
		t.Fatal(err)
	}
	if len(store) != before {
		t.Fatalf("dedup failed: store grew from %d to %d", before, len(store))
	}
}

func TestManifestWireShape(t *testing.T) {
	// The object CID is the hash of this exact manifest JSON; its shape must be stable/portable.
	m := manifest{Kind: manifestKind, ChunkSize: DefaultChunkSize, TotalSize: 5, Chunks: []string{"aa", "bb"}}
	b, _ := json.Marshal(m)
	want := `{"kind":"ce-object-v1","chunk_size":1048576,"total_size":5,"chunks":["aa","bb"]}`
	if string(b) != want {
		t.Fatalf("manifest JSON = %s\nwant %s", b, want)
	}
}

func TestJobsDecode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A job with an optional cost present as a base-unit string.
		w.Write([]byte(`[{"job_id":"j1","status":"running","cost":"2500000000000000000"}]`))
	}))
	defer srv.Close()
	c := Connect(WithBaseURL(srv.URL), WithToken("t"))
	jobs, err := c.Jobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].JobID != "j1" || jobs[0].Status != "running" {
		t.Fatalf("bad jobs: %+v", jobs)
	}
	if jobs[0].Cost == nil || jobs[0].Cost.Credits() != "2.5" {
		t.Fatalf("job cost decode: %+v", jobs[0].Cost)
	}
	if jobs[0].Payer != nil {
		t.Fatalf("absent payer should be nil, got %v", *jobs[0].Payer)
	}
}

func TestEconomyDisabledDetection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("economy disabled"))
	}))
	defer srv.Close()
	c := Connect(WithBaseURL(srv.URL), WithToken("t"))
	_, err := c.Transfer(context.Background(), "peer", FromCredits(1))
	if err == nil {
		t.Fatal("expected error from economy-off node")
	}
	if !IsEconomyDisabled(err) {
		t.Fatalf("IsEconomyDisabled should be true for a 503, got %v", err)
	}
}

func TestStatusBalanceBreakdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"node_id":"n","height":3,"difficulty":8,"balance":"1000000000000000000","free":"1000000000000000000","economy":false}`))
	}))
	defer srv.Close()
	c := Connect(WithBaseURL(srv.URL), WithToken("t"))
	s, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Balance.Credits() != "1" {
		t.Fatalf("balance = %s, want 1", s.Balance.Credits())
	}
	if s.Free == nil || s.Free.Credits() != "1" {
		t.Fatalf("free breakdown = %v", s.Free)
	}
	if s.LockedBond != nil {
		t.Fatalf("absent locked_bond should be nil")
	}
	if s.EconomyEnabled() {
		t.Fatalf("economy:false should mean EconomyEnabled()=false")
	}
}
