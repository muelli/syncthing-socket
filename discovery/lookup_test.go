package discovery

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const testDeviceID = "AAAAAAA-BBBBBBB-CCCCCCC-DDDDDDD-EEEEEEE-FFFFFFF-GGGGGGG-HHHHHHH"

func TestLookupRecordParsesRecord(t *testing.T) {
	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`{"seen":"2026-01-02T03:04:05Z","addresses":["tcp://192.0.2.1:22000","relay://192.0.2.2:22067"]}`))
	}))
	defer ts.Close()

	rec, err := LookupRecord(context.Background(), testDeviceID, ts.URL+"/v2/")
	if err != nil {
		t.Fatalf("LookupRecord: %v", err)
	}
	if want := "device=" + testDeviceID; gotQuery != want {
		t.Errorf("query = %q, want %q", gotQuery, want)
	}
	if want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC); !rec.Seen.Equal(want) {
		t.Errorf("Seen = %v, want %v", rec.Seen, want)
	}
	if len(rec.Addresses) != 2 || rec.Addresses[0] != "tcp://192.0.2.1:22000" || rec.Addresses[1] != "relay://192.0.2.2:22067" {
		t.Errorf("Addresses = %v", rec.Addresses)
	}
}

// A server URL that already carries a query string gets the device appended with "&",
// which is how a self-hosted discovery server with its own parameters is addressed.
func TestLookupRecordAppendsToExistingQuery(t *testing.T) {
	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`{"addresses":[]}`))
	}))
	defer ts.Close()

	if _, err := LookupRecord(context.Background(), testDeviceID, ts.URL+"/v2/?id=abc"); err != nil {
		t.Fatalf("LookupRecord: %v", err)
	}
	if want := "id=abc&device=" + testDeviceID; gotQuery != want {
		t.Errorf("query = %q, want %q", gotQuery, want)
	}
}

func TestLookupRecordNotFoundIsNotAnError(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()

	rec, err := LookupRecord(context.Background(), testDeviceID, ts.URL)
	if err != nil || rec != nil {
		t.Fatalf("LookupRecord = (%v, %v), want (nil, nil)", rec, err)
	}
}

func TestLookupRecordOtherStatusIsAnError(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusBadRequest, http.StatusTooManyRequests, http.StatusInternalServerError} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		rec, err := LookupRecord(context.Background(), testDeviceID, ts.URL)
		ts.Close()
		if err == nil {
			t.Errorf("HTTP %d: got record %v and no error", status, rec)
		}
	}
}

// An undatable record keeps its addresses: the caller reports "announced, age unknown"
// rather than failing.
func TestLookupRecordUnparseableSeen(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"seen":"yesterday-ish","addresses":["tcp://192.0.2.1:22000"]}`))
	}))
	defer ts.Close()

	rec, err := LookupRecord(context.Background(), testDeviceID, ts.URL)
	if err != nil {
		t.Fatalf("LookupRecord: %v", err)
	}
	if !rec.Seen.IsZero() {
		t.Errorf("Seen = %v, want zero", rec.Seen)
	}
	if len(rec.Addresses) != 1 || rec.Addresses[0] != "tcp://192.0.2.1:22000" {
		t.Errorf("Addresses = %v", rec.Addresses)
	}
}

func TestLookupRecordAbsentSeen(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"addresses":["tcp://192.0.2.1:22000"]}`))
	}))
	defer ts.Close()

	rec, err := LookupRecord(context.Background(), testDeviceID, ts.URL)
	if err != nil {
		t.Fatalf("LookupRecord: %v", err)
	}
	if !rec.Seen.IsZero() {
		t.Errorf("Seen = %v, want zero", rec.Seen)
	}
	if len(rec.Addresses) != 1 {
		t.Errorf("Addresses = %v", rec.Addresses)
	}
}

func TestLookupRecordMalformedJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"addresses":[`))
	}))
	defer ts.Close()

	if rec, err := LookupRecord(context.Background(), testDeviceID, ts.URL); err == nil {
		t.Fatalf("got record %v and no error for malformed JSON", rec)
	}
}

func TestLookupRecordHonoursContext(t *testing.T) {
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer ts.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := LookupRecord(ctx, testDeviceID, ts.URL)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("lookup took %v, the context deadline was not honoured", elapsed)
	}
}

// The test server's certificate is self-signed, so a lookup that verifies it must fail.
func TestLookupRecordVerifiesTLS(t *testing.T) {
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"addresses":["tcp://192.0.2.1:22000"]}`))
	}))
	ts.Config.ErrorLog = log.New(io.Discard, "", 0) // the rejected handshake is expected
	ts.StartTLS()
	defer ts.Close()

	rec, err := LookupRecord(context.Background(), testDeviceID, ts.URL)
	var verr *tls.CertificateVerificationError
	if !errors.As(err, &verr) {
		t.Fatalf("LookupRecord = (%v, %v), want a certificate verification error", rec, err)
	}
}

func TestDiscoveryDefaults(t *testing.T) {
	if DefaultURL != "https://discovery-lookup.syncthing.net/v2/" {
		t.Errorf("DefaultURL = %q", DefaultURL)
	}
	if DefaultAnnounceInterval != 30*time.Minute {
		t.Errorf("DefaultAnnounceInterval = %v", DefaultAnnounceInterval)
	}
	if lookupTimeout != 10*time.Second {
		t.Errorf("lookupTimeout = %v", lookupTimeout)
	}
}
