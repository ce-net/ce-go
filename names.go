package ce

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// ClaimName claims a human-readable name for this node (POST /names/claim).
func (c *Client) ClaimName(ctx context.Context, name string) error {
	_, err := c.do(ctx, http.MethodPost, "/names/claim", map[string]any{"name": name})
	return err
}

// ResolveName resolves a name to a node id (GET /names/:name). The bool is false when the name is
// unclaimed (404), with a nil error.
func (c *Client) ResolveName(ctx context.Context, name string) (string, bool, error) {
	raw, err := c.do(ctx, http.MethodGet, "/names/"+url.PathEscape(name), nil)
	if err != nil {
		if e, ok := err.(*Error); ok && e.Status == http.StatusNotFound {
			return "", false, nil
		}
		return "", false, err
	}
	var r struct {
		NodeID string `json:"node_id"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", false, err
	}
	return r.NodeID, r.NodeID != "", nil
}

// AdvertiseService advertises this node as a provider of service (POST /discovery/advertise).
func (c *Client) AdvertiseService(ctx context.Context, service string) error {
	_, err := c.do(ctx, http.MethodPost, "/discovery/advertise", map[string]any{"service": service})
	return err
}

// FindService returns the node ids advertising service (GET /discovery/find/:service). The service
// name may contain '/', so it is percent-encoded as a single path segment.
func (c *Client) FindService(ctx context.Context, service string) ([]string, error) {
	raw, err := c.do(ctx, http.MethodGet, "/discovery/find/"+encodeSegment(service), nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Providers []string `json:"providers"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return r.Providers, nil
}

// tagPrefix namespaces tag advertisements within the discovery keyspace, matching ce-rs/ce-ts.
const tagPrefix = "tag:"

// AdvertiseTag advertises this node under a tag (e.g. "region:eu"), layered on discovery.
func (c *Client) AdvertiseTag(ctx context.Context, tag string) error {
	return c.AdvertiseService(ctx, tagPrefix+tag)
}

// FindTag returns node ids advertising a tag.
func (c *Client) FindTag(ctx context.Context, tag string) ([]string, error) {
	return c.FindService(ctx, tagPrefix+tag)
}

// encodeSegment percent-encodes a discovery name as one path segment, encoding '/' as %2F so
// "app/role" style service names route correctly. Matches ce-rs encode_path_segment.
func encodeSegment(s string) string {
	return strings.ReplaceAll(url.PathEscape(s), "/", "%2F")
}
