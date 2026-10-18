// SPDX-License-Identifier: AGPL-3.0-or-later

// Package discovery looks up devices on Syncthing's global discovery service.
//
// It resolves a Device ID to the addresses the device last announced, and to when it last
// announced them. It imports only the standard library, so other programs can depend on it
// without pulling in the syncthing-socket CLI, WebRTC, or the Syncthing libraries.
package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// DefaultURL is the Syncthing global discovery endpoint used to resolve a peer's Device ID
// to its current addresses. Callers should pass it rather than an empty string, which
// produces a relative URL and fails every lookup.
const DefaultURL = "https://discovery-lookup.syncthing.net/v2/"

// DefaultAnnounceInterval is how often a long-running server refreshes its discovery
// record. It is deliberately lazy: the record outlives it comfortably, and these are
// shared public servers.
const DefaultAnnounceInterval = 30 * time.Minute

// lookupTimeout bounds a whole LookupRecord request, on top of any context deadline.
const lookupTimeout = 10 * time.Second

// Record is what global discovery knows about a device.
//
// Seen is the part that matters to a key holder deciding whether to bother trying: the
// record itself outlives the machine by over an hour, so its presence says only that the
// machine announced at some point, while Seen says when.
type Record struct {
	Seen      time.Time
	Addresses []string
}

// LookupRecord fetches the full discovery record, timestamp included.
//
// It returns (nil, nil) when the server answers 404, which is the ordinary case of a
// device that has never announced or whose record has expired. It verifies the discovery
// server's TLS certificate.
func LookupRecord(ctx context.Context, serverID string, discoveryServer string) (*Record, error) {
	urlStr := fmt.Sprintf("%s&device=%s", discoveryServer, serverID)
	if !strings.Contains(discoveryServer, "?") {
		urlStr = fmt.Sprintf("%s?device=%s", discoveryServer, serverID)
	}

	client := &http.Client{Timeout: lookupTimeout}
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Not an error: a machine that has never announced, or has been gone long enough for
	// the record to expire, is the ordinary "nothing is waiting" case.
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery lookup returned HTTP %d", resp.StatusCode)
	}

	var body struct {
		Seen      string   `json:"seen"`
		Addresses []string `json:"addresses"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	record := &Record{Addresses: body.Addresses}
	if body.Seen != "" {
		// A record we cannot date is treated as undated rather than as an error: the
		// addresses are still usable, and the caller reports "announced, age unknown".
		if seen, err := time.Parse(time.RFC3339, body.Seen); err == nil {
			record.Seen = seen
		}
	}
	return record, nil
}
