package ce

import (
	"context"
	"encoding/json"
	"net/http"
)

// The paid job market (BidSpec/Job, Bid/Jobs/Job/Kill) is NOT a substrate concept — money and the
// compute market belong to the economy ceapp. That surface moved to the economy adapter's Go SDK
// (github.com/ce-net/economy-adapter/clients/go, EconomyClient). The core substrate SDK keeps only
// the capacity atlas and the beacon below (placement + verifiable randomness are substrate
// primitives any app — economy or not — reads).

// AtlasEntry is one peer's advertised capacity (GET /atlas).
type AtlasEntry struct {
	NodeID       string   `json:"node_id"`
	CPUCores     uint32   `json:"cpu_cores"`
	MemMB        uint32   `json:"mem_mb"`
	RunningJobs  uint32   `json:"running_jobs"`
	LastSeenSecs uint64   `json:"last_seen_secs"`
	Tags         []string `json:"tags"`
}

// Atlas returns the peer capacity atlas (GET /atlas).
func (c *Client) Atlas(ctx context.Context) ([]AtlasEntry, error) {
	raw, err := c.do(ctx, http.MethodGet, "/atlas", nil)
	if err != nil {
		return nil, err
	}
	var a []AtlasEntry
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	return a, nil
}

// Beacon is verifiable public randomness derived from the chain tip (GET /beacon).
type Beacon struct {
	Height uint64 `json:"height"`
	Hash   string `json:"hash"`
}

// Beacon returns the current beacon (GET /beacon).
func (c *Client) Beacon(ctx context.Context) (Beacon, error) {
	raw, err := c.do(ctx, http.MethodGet, "/beacon", nil)
	if err != nil {
		return Beacon{}, err
	}
	var b Beacon
	if err := json.Unmarshal(raw, &b); err != nil {
		return Beacon{}, err
	}
	return b, nil
}
