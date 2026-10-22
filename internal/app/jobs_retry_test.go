package app

import (
	"net/http"
	"testing"
)

func TestRetryJobRequiresDatabase(t *testing.T) {
	r := testRouter(testServer())
	w := doRequest(r, http.MethodPost, "/api/v1/jobs/job-1/retry", "", nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("retry without database status = %d, want 503", w.Code)
	}
}
