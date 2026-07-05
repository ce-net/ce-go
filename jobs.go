package ce

import (
	"context"
	"encoding/json"
	"net/http"
)

// BidSpec describes a container job to bid for.
type BidSpec struct {
	Image        string   `json:"image"`
	Cmd          []string `json:"cmd"`
	CPUCores     uint32   `json:"cpu_cores"`
	MemMB        uint64   `json:"mem_mb"`
	DurationSecs uint64   `json:"duration_secs"`
	Bid          Amount   `json:"bid"`
}

// Job is a container job's state. Optional fields are pointers (absent until the job has a payer,
// container, or settled cost).
type Job struct {
	JobID       string  `json:"job_id"`
	Status      string  `json:"status"`
	Payer       *string `json:"payer"`
	ContainerID *string `json:"container_id"`
	Cost        *Amount `json:"cost"`
	Bid         *Amount `json:"bid"`
}

// Bid submits a container job bid (POST /jobs/bid) and returns the job id. Economy-gated: returns
// a 503 *Error on an economy-off node (see IsEconomyDisabled).
func (c *Client) Bid(ctx context.Context, spec BidSpec) (string, error) {
	raw, err := c.do(ctx, http.MethodPost, "/jobs/bid", spec)
	if err != nil {
		return "", err
	}
	var r struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", err
	}
	return r.JobID, nil
}

// Jobs lists all jobs (GET /jobs).
func (c *Client) Jobs(ctx context.Context) ([]Job, error) {
	raw, err := c.do(ctx, http.MethodGet, "/jobs", nil)
	if err != nil {
		return nil, err
	}
	var jobs []Job
	if err := json.Unmarshal(raw, &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

// Job fetches one job's state (GET /jobs/:id).
func (c *Client) Job(ctx context.Context, jobID string) (Job, error) {
	raw, err := c.do(ctx, http.MethodGet, "/jobs/"+jobID, nil)
	if err != nil {
		return Job{}, err
	}
	var j Job
	if err := json.Unmarshal(raw, &j); err != nil {
		return Job{}, err
	}
	return j, nil
}

// Kill force-stops a job's container (DELETE /jobs/:id).
func (c *Client) Kill(ctx context.Context, jobID string) error {
	_, err := c.do(ctx, http.MethodDelete, "/jobs/"+jobID, nil)
	return err
}

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
