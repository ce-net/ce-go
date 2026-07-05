package ce

import (
	"context"
	"encoding/json"
	"net/http"
)

// IsEconomyDisabled reports whether err is the node refusing an economic operation because it runs
// in personal-mesh mode (economy off) — a 503 from the node. Apps should treat this as "economy
// not available here" and degrade, not as a hard failure. Economy is being extracted into an
// adapter and will eventually leave the substrate entirely.
func IsEconomyDisabled(err error) bool {
	e, ok := err.(*Error)
	return ok && e.Status == http.StatusServiceUnavailable
}

// Transfer moves credits to another node (POST /transfer) and returns the tx id. Economy-gated.
func (c *Client) Transfer(ctx context.Context, to string, amount Amount) (string, error) {
	raw, err := c.do(ctx, http.MethodPost, "/transfer", map[string]any{"to": to, "amount": amount})
	if err != nil {
		return "", err
	}
	var r struct {
		TxID string `json:"tx_id"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", err
	}
	return r.TxID, nil
}

// Channel is an open payment channel (GET /channels).
type Channel struct {
	ChannelID    string `json:"channel_id"`
	Payer        string `json:"payer"`
	Host         string `json:"host"`
	Capacity     Amount `json:"capacity"`
	ExpiryHeight uint64 `json:"expiry_height"`
}

// Channels lists open payment channels (GET /channels).
func (c *Client) Channels(ctx context.Context) ([]Channel, error) {
	raw, err := c.do(ctx, http.MethodGet, "/channels", nil)
	if err != nil {
		return nil, err
	}
	var ch []Channel
	if err := json.Unmarshal(raw, &ch); err != nil {
		return nil, err
	}
	return ch, nil
}

// OpenChannel opens a payment channel to host, locking capacity until expiryHeight (POST
// /channels/open). Economy-gated. Returns the channel id.
func (c *Client) OpenChannel(ctx context.Context, host string, capacity Amount, expiryHeight uint64) (string, error) {
	raw, err := c.do(ctx, http.MethodPost, "/channels/open", map[string]any{
		"host": host, "capacity": capacity, "expiry_height": expiryHeight,
	})
	if err != nil {
		return "", err
	}
	var r struct {
		ChannelID string `json:"channel_id"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", err
	}
	return r.ChannelID, nil
}

// Receipt is an off-chain payment receipt the payer signs (POST /channels/receipt).
type Receipt struct {
	ChannelID  string `json:"channel_id"`
	Cumulative Amount `json:"cumulative"`
	PayerSig   string `json:"payer_sig"`
}

// SignReceipt signs an off-chain receipt raising the channel's cumulative paid amount.
func (c *Client) SignReceipt(ctx context.Context, channelID, host string, cumulative Amount) (Receipt, error) {
	raw, err := c.do(ctx, http.MethodPost, "/channels/receipt", map[string]any{
		"channel_id": channelID, "host": host, "cumulative": cumulative,
	})
	if err != nil {
		return Receipt{}, err
	}
	var r Receipt
	if err := json.Unmarshal(raw, &r); err != nil {
		return Receipt{}, err
	}
	return r, nil
}

// CloseChannel redeems the highest receipt to settle a channel (POST /channels/:id/close).
func (c *Client) CloseChannel(ctx context.Context, channelID string, cumulative Amount, payerSig string) error {
	_, err := c.do(ctx, http.MethodPost, "/channels/"+channelID+"/close", map[string]any{
		"cumulative": cumulative, "payer_sig": payerSig,
	})
	return err
}

// ExpireChannel reclaims a channel's remaining capacity after expiry (POST /channels/:id/expire).
func (c *Client) ExpireChannel(ctx context.Context, channelID string) error {
	_, err := c.do(ctx, http.MethodPost, "/channels/"+channelID+"/expire", nil)
	return err
}

// History is a node's cumulative interaction record (GET /history/:node_id) — the reputation
// substrate.
type History struct {
	NodeID           string `json:"node_id"`
	JobsHosted       uint64 `json:"jobs_hosted"`
	JobsPaid         uint64 `json:"jobs_paid"`
	HeartbeatsHosted uint64 `json:"heartbeats_hosted"`
	HeartbeatsPaid   uint64 `json:"heartbeats_paid"`
	Expiries         uint64 `json:"expiries"`
	Earned           Amount `json:"earned"`
	Spent            Amount `json:"spent"`
	FirstHeight      uint64 `json:"first_height"`
	LastHeight       uint64 `json:"last_height"`
}

// History returns a node's interaction history (GET /history/:node_id).
func (c *Client) History(ctx context.Context, nodeID string) (History, error) {
	raw, err := c.do(ctx, http.MethodGet, "/history/"+nodeID, nil)
	if err != nil {
		return History{}, err
	}
	var h History
	if err := json.Unmarshal(raw, &h); err != nil {
		return History{}, err
	}
	return h, nil
}
