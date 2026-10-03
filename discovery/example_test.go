package discovery_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/muelli/syncthing-socket/discovery"
)

func ExampleLookupRecord() {
	// A stand-in for the global discovery server. Real callers pass discovery.DefaultURL.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"seen":"2026-01-02T03:04:05Z","addresses":["tcp://192.0.2.1:22000"]}`)
	}))
	defer ts.Close()

	rec, err := discovery.LookupRecord(context.Background(), "MFZWI3D-BONSGYC-YLTMRWG-C43ENR5-QXGZDMM-FZWI3DP-BONSGYY-LTMRWAD", ts.URL+"/v2/")
	if err != nil {
		fmt.Println("lookup failed:", err)
		return
	}
	if rec == nil {
		fmt.Println("device has not announced")
		return
	}
	fmt.Println(rec.Seen.Format("2006-01-02 15:04:05"), rec.Addresses)
	// Output: 2026-01-02 03:04:05 [tcp://192.0.2.1:22000]
}
