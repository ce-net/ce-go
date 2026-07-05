package ce

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
)

// DefaultChunkSize is the object chunk size (1 MiB). It MUST match every other CE SDK so that a
// content id computed anywhere refers to the same bytes.
const DefaultChunkSize = 1 << 20

// CID returns the content id (lowercase hex SHA-256) of data — identical across all CE SDKs and
// to the hash the node returns from POST /blobs.
func CID(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// PutBlob stores raw bytes in the node's content-addressed store and returns their SHA-256 hash.
func (c *Client) PutBlob(ctx context.Context, data []byte) (string, error) {
	raw, err := c.doRaw(ctx, http.MethodPost, "/blobs", data)
	if err != nil {
		return "", err
	}
	var r struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", err
	}
	return r.Hash, nil
}

// GetBlob fetches a blob by its hash.
func (c *Client) GetBlob(ctx context.Context, hash string) ([]byte, error) {
	return c.doRaw(ctx, http.MethodGet, "/blobs/"+hash, nil)
}

// manifest is the object descriptor. Field order and names are wire-compatible with ce-rs so an
// object CID (the hash of this manifest) is identical across SDKs.
type manifest struct {
	Kind      string   `json:"kind"`
	ChunkSize uint64   `json:"chunk_size"`
	TotalSize uint64   `json:"total_size"`
	Chunks    []string `json:"chunks"`
}

const manifestKind = "ce-object-v1"

// PutObject stores arbitrary-size data as content-addressed chunks plus a manifest, returning the
// object CID (the manifest's hash). Chunks are uploaded in order; a CID-keyed store dedupes.
func (c *Client) PutObject(ctx context.Context, data []byte) (string, error) {
	chunks := make([]string, 0, len(data)/DefaultChunkSize+1)
	for off := 0; off < len(data); off += DefaultChunkSize {
		end := off + DefaultChunkSize
		if end > len(data) {
			end = len(data)
		}
		h, err := c.PutBlob(ctx, data[off:end])
		if err != nil {
			return "", err
		}
		chunks = append(chunks, h)
	}
	m := manifest{Kind: manifestKind, ChunkSize: DefaultChunkSize, TotalSize: uint64(len(data)), Chunks: chunks}
	mb, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return c.PutBlob(ctx, mb)
}

// GetObject reassembles an object by CID, verifying every chunk against its hash.
func (c *Client) GetObject(ctx context.Context, cid string) ([]byte, error) {
	mb, err := c.GetBlob(ctx, cid)
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(mb, &m); err != nil {
		return nil, fmt.Errorf("object %s: bad manifest: %w", cid, err)
	}
	if m.Kind != manifestKind {
		return nil, fmt.Errorf("object %s: not a ce object (kind %q)", cid, m.Kind)
	}
	var buf bytes.Buffer
	buf.Grow(int(m.TotalSize))
	for _, h := range m.Chunks {
		chunk, err := c.GetBlob(ctx, h)
		if err != nil {
			return nil, err
		}
		if got := CID(chunk); got != h {
			return nil, fmt.Errorf("object %s: chunk cid mismatch: got %s want %s", cid, got, h)
		}
		buf.Write(chunk)
	}
	return buf.Bytes(), nil
}
