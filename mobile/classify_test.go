package mobile

import "testing"

func TestClassifyUnlockError(t *testing.T) {
	cases := map[string]string{
		"client failed: discovery lookup failed: device ABC not found in discovery":                              ErrServerOffline,
		"client failed: failed to get invitation from relay 1.2.3.4:22067: incorrect response code 1: not found": ErrServerUnreachable,
		"client failed: security mismatch: connected to device X, expected Y":                                    ErrWrongDevice,
		"client failed: discovery lookup failed: dial tcp: lookup x on [::1]:53":                                 ErrNoNetwork,
		"client failed: authentication failed: bad totp":                                                         ErrRejected,
		"client failed: something nobody predicted":                                                              ErrUnknown,
	}
	for msg, want := range cases {
		if got := ClassifyUnlockError(msg); got != want {
			t.Errorf("ClassifyUnlockError(%q) = %s, want %s", msg, got, want)
		}
	}
}
