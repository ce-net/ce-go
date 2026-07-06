// Tier-B demo / live check: exercises the substrate full-node surface of ce-go against a live local
// node — content-addressed blob + object round-trips (deterministic), the capacity atlas, the
// beacon, and CEP-1 signals. The MONEY surface (transfer/jobs/channels) is not substrate; it lives
// in the economy adapter's Go SDK (github.com/ce-net/economy-adapter/clients/go) with its own demo.
//
//	go run ./examples/tierb
package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"time"

	ce "github.com/ce-net/ce-go"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := ce.Connect()
	if err := c.WaitReady(ctx, 15*time.Second); err != nil {
		log.Fatalf("node not ready: %v", err)
	}

	s, err := c.Status(ctx)
	if err != nil {
		log.Fatalf("status: %v", err)
	}
	fmt.Printf("node=%s peer=%s port=%d economy=%v\n", s.NodeID, s.PeerID, s.ListenPort, s.EconomyEnabled())

	// Blob round-trip: the node's returned hash must equal our locally computed CID.
	blob := []byte("hello ce-go tier b")
	hash, err := c.PutBlob(ctx, blob)
	if err != nil {
		log.Fatalf("put_blob: %v", err)
	}
	if hash != ce.CID(blob) {
		log.Fatalf("hash mismatch: node=%s local=%s", hash, ce.CID(blob))
	}
	back, err := c.GetBlob(ctx, hash)
	if err != nil || !bytes.Equal(back, blob) {
		log.Fatalf("get_blob round-trip failed: %v", err)
	}
	fmt.Printf("blob ok: %s (cid == node hash, round-trip exact)\n", hash[:16]+"...")

	// Object round-trip across multiple chunks.
	obj := make([]byte, ce.DefaultChunkSize*2+321)
	for i := range obj {
		obj[i] = byte(i * 3)
	}
	cid, err := c.PutObject(ctx, obj)
	if err != nil {
		log.Fatalf("put_object: %v", err)
	}
	got, err := c.GetObject(ctx, cid)
	if err != nil || !bytes.Equal(got, obj) {
		log.Fatalf("get_object round-trip failed: %v", err)
	}
	fmt.Printf("object ok: %s (%d bytes, %d chunks, all cid-verified)\n", cid[:16]+"...", len(obj), len(obj)/ce.DefaultChunkSize+1)

	// Discovery (informational — DHT propagation is async).
	if err := c.AdvertiseService(ctx, "ce-go-livecheck"); err != nil {
		fmt.Printf("advertise: %v\n", err)
	}
	if atlas, err := c.Atlas(ctx); err == nil {
		fmt.Printf("atlas: %d peers\n", len(atlas))
	}
	if b, err := c.Beacon(ctx); err == nil {
		fmt.Printf("beacon: height=%d hash=%s\n", b.Height, short(b.Hash))
	}
	if sigs, err := c.Signals(ctx); err == nil {
		fmt.Printf("signals: %d recent\n", len(sigs))
	}

	// A name that is almost certainly unclaimed resolves to (·, false, nil).
	if id, ok, err := c.ResolveName(ctx, "no-such-name-"+cid[:8]); err == nil {
		fmt.Printf("resolve unclaimed name: found=%v id=%q\n", ok, id)
	}

	fmt.Println("tier-b substrate live check complete")
}

func short(h string) string {
	if len(h) > 16 {
		return h[:16] + "..."
	}
	return h
}
