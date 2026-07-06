---
name: ce-go
description: How to use and work on ce-go — the stdlib-only Go SDK for a CE mesh node. Read before writing a Go ceapp or editing this repo. Ships with the repo (self-contained).
---

# ce-go — the Go SDK for CE

A dependency-free (stdlib-only) Go client for a local CE node. It is a thin HTTP+SSE wrapper over the
node's local API (`127.0.0.1:8844`); the node does the heavy work (mesh routing, NAT, crypto, blobs),
so ce-go just marshals JSON+hex and waits on I/O. Import: `ce "github.com/ce-net/ce-go"`.

## Use it

```go
ctx := context.Background()
c := ce.Connect()                                  // auto-discovers the bearer api.token
c.WaitReady(ctx, 30*time.Second)                   // don't race a node started at boot
c.Publish(ctx, "building/temp", []byte("21.5"))    // fan-out telemetry
msgs, _ := c.Subscribe(ctx, "building/temp")        // <-chan Message
c.Serve(ctx, []string{"demo/echo"},                 // be a mesh responder (a capability)
    func(m ce.Message) ([]byte, error) { return m.Payload, nil })
reply, _ := c.Request(ctx, peerID, "demo/echo", []byte("hi"), 10*time.Second)   // reliable RPC
```

**Tier A** (the app surface): `Connect`/`Status`/`WaitReady`, `Publish`/`Subscribe`, `Send`/`Request`/
`Reply`, `Serve`. **Tier B** (full node): `PutBlob`/`GetBlob`/`CID`, `PutObject`/`GetObject` (chunked,
CID-verified), `Bid`/`Jobs`/`Kill`, `Atlas`/`Beacon`, names/discovery/tags, economy
(`Transfer`/`Channels`/`History`, 503-tolerant), typed SSE streams (`Blocks`/`Transactions`/
`SignalStream`/`Signals`). See `README.md` for the full table, `DESIGN.md` for the why, `IDEAS.md` for
what the system makes possible.

## Rules that bite (the wire contract)

- Payloads are `[]byte`, hex-encoded on the wire. Never base64.
- Money is `Amount` — integer base units (`math/big`), serialized as a decimal STRING, never a float
  or JSON number (values exceed 2^53). `1 credit = 10^18` base units.
- `CID(data)` = the node's `POST /blobs` hash; `PutObject` uses a byte-stable 1 MiB-chunk manifest so
  an object CID is IDENTICAL across every CE SDK. Do not change the manifest bytes in `data.go`.
- `Status` is substrate-only (`NodeID`, `PeerID`, `ListenPort`, `Economy`). The ledger
  (height/balance) is the economy adapter's, NOT here. Economic calls return a 503 `*Error` on a core
  node — check `IsEconomyDisabled(err)` and degrade.
- The SDK holds NO keys and does NO crypto — the node signs/verifies. Never add crypto here.

## Work on it

```sh
go build ./... && go vet ./... && go test ./...     # stdlib only, fast; tests use httptest fakes
go run ./examples/quickstart                         # against a live node (ce start)
```

Idiomatic Go, not a transpile (`(T, error)` + `ctx` + channels). Reproduce-first for bugs: add the
failing test before the fix. The REAL cross-language gate is the `ce-conformance` kit (a separate
repo) — ce-go must stay green in its matrix.
