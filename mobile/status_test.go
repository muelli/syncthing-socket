package mobile

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A verbatim response from the public discovery server. The "seen" field is the whole
// basis of the readiness states, so pin its shape.
const liveDiscoveryBody = `{"seen":"2026-09-07T17:17:20Z","addresses":` +
	`["relay://184.23.60.70:22067/?id=IYIBOKE-UGTUXE2-N2MBPWP-6W7PATR-KVSPKGO-2CN5UD5-KSMUC2S-J3L3JAD"]}`

func TestFreshRecordIsWaiting(t *testing.T) {
	st := statusForSeen(t, time.Now().Add(-20*time.Second))
	if st.State != StatusWaiting {
		t.Fatalf("a 20s old record gave %q, want %q", st.State, StatusWaiting)
	}
	if st.AgeSeconds < 15 || st.AgeSeconds > 30 {
		t.Errorf("AgeSeconds = %d, want about 20", st.AgeSeconds)
	}
}

// The mistake this whole design exists to avoid: discovery keeps a record for more than an
// hour after the machine stops announcing, so an old record must never read as "waiting".
// Without this distinction the app would tell you your computer is at its prompt long
// after it had finished booting.
func TestOldRecordIsStaleNotWaiting(t *testing.T) {
	for _, age := range []time.Duration{5 * time.Minute, 30 * time.Minute, 55 * time.Minute} {
		st := statusForSeen(t, time.Now().Add(-age))
		if st.State != StatusStale {
			t.Errorf("a record %s old gave %q, want %q", age, st.State, StatusStale)
		}
	}
}

// The machine announces every 60s while waiting, so a single missed announcement must not
// flip a waiting machine to stale.
func TestOneMissedAnnouncementIsStillWaiting(t *testing.T) {
	st := statusForSeen(t, time.Now().Add(-125*time.Second))
	if st.State != StatusWaiting {
		t.Fatalf("a 125s old record gave %q, want %q; one missed announcement must not "+
			"flip a waiting machine to stale", st.State, StatusWaiting)
	}
}

// A phone whose clock runs behind the discovery server would otherwise compute a negative
// age and report a live machine as stale.
func TestClockSkewDoesNotBreakFreshness(t *testing.T) {
	st := statusForSeen(t, time.Now().Add(90*time.Second))
	if st.State != StatusWaiting {
		t.Fatalf("a record from the future gave %q, want %q", st.State, StatusWaiting)
	}
	if st.AgeSeconds != 0 {
		t.Errorf("AgeSeconds = %d, want 0 for a future timestamp", st.AgeSeconds)
	}
}

// 404 is the ordinary "nothing is waiting" answer, not an error.
func TestNoRecordIsAbsent(t *testing.T) {
	st := statusFromServer(t, http.StatusNotFound, "Not Found")
	if st.State != StatusAbsent {
		t.Fatalf("a 404 gave %q, want %q", st.State, StatusAbsent)
	}
	if st.AgeSeconds != -1 {
		t.Errorf("AgeSeconds = %d, want -1 when there is no record", st.AgeSeconds)
	}
}

// "Cannot reach discovery" must not be shown as "the machine is not there". They call for
// opposite actions: fix your phone's network, versus go and look at the computer.
func TestUnreachableDiscoveryIsNotAbsent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := server.URL + "/v2/"
	server.Close() // nothing is listening now

	st, err := checkAgainst(url, "AAAA-BBBB")
	if err != nil {
		t.Fatalf("CheckServerStatus: %v", err)
	}
	if st.State != StatusNoNetwork {
		t.Fatalf("an unreachable discovery server gave %q, want %q", st.State, StatusNoNetwork)
	}
}

// A record whose timestamp we cannot read still exists, so it is stale rather than absent,
// and its age is unknown rather than guessed at.
func TestUndatedRecordIsStale(t *testing.T) {
	st := statusFromServer(t, http.StatusOK, `{"addresses":["relay://x:1/?id=y"]}`)
	if st.State != StatusStale {
		t.Fatalf("an undated record gave %q, want %q", st.State, StatusStale)
	}
	if st.AgeSeconds != -1 {
		t.Errorf("AgeSeconds = %d, want -1 when the age is unknown", st.AgeSeconds)
	}
}

func TestEmptyDeviceIDIsRejected(t *testing.T) {
	if _, err := CheckServerStatus("   "); err == nil {
		t.Fatal("an empty device ID was accepted")
	}
}

// The real response body must parse, so a change in its shape is caught here rather than
// by a phone showing the wrong colour.
func TestRealDiscoveryBodyParses(t *testing.T) {
	st := statusFromServer(t, http.StatusOK, liveDiscoveryBody)
	if st.State != StatusStale {
		t.Fatalf("state = %q; the pinned body is from the past so it should be stale", st.State)
	}
	if st.AgeSeconds < 0 {
		t.Errorf("AgeSeconds = %d, want a real age parsed from the body", st.AgeSeconds)
	}
}

func statusForSeen(t *testing.T, seen time.Time) *ServerStatus {
	t.Helper()
	body := `{"seen":"` + seen.UTC().Format(time.RFC3339) + `","addresses":["relay://x:1/?id=y"]}`
	return statusFromServer(t, http.StatusOK, body)
}

func statusFromServer(t *testing.T, code int, body string) *ServerStatus {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	st, err := checkAgainst(server.URL+"/v2/", "AAAA-BBBB")
	if err != nil {
		t.Fatalf("CheckServerStatus: %v", err)
	}
	return st
}
