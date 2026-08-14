package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestIdleWriterSurvivesWriteTimeout reproduces the preview/large-download
// scenario: the handler spends longer than the server's absolute WriteTimeout
// before writing the first byte (a slow conversion), then streams. With
// IdleWriter the response must still arrive intact; the plain control must not.
func TestIdleWriterSurvivesWriteTimeout(t *testing.T) {
	const body = "rendered-after-the-write-timeout-elapsed"
	const writeTimeout = 100 * time.Millisecond
	const work = 300 * time.Millisecond // > writeTimeout, before any write

	run := func(useIdle bool) (string, error) {
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(work)
			var dst io.Writer = w
			if useIdle {
				dst = NewIdleWriter(w, 5*time.Second)
			}
			_, _ = io.Copy(dst, strings.NewReader(body))
		})
		srv := httptest.NewUnstartedServer(h)
		srv.Config.WriteTimeout = writeTimeout
		srv.Start()
		defer srv.Close()

		resp, err := http.Get(srv.URL)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		return string(b), err
	}

	// With IdleWriter: full body delivered despite the work exceeding WriteTimeout.
	got, err := run(true)
	if err != nil {
		t.Fatalf("idle writer: unexpected error: %v", err)
	}
	if got != body {
		t.Fatalf("idle writer: got %q, want %q", got, body)
	}

	// Control (no IdleWriter): the absolute WriteTimeout should truncate it.
	ctrl, ctrlErr := run(false)
	if ctrlErr == nil && ctrl == body {
		t.Fatalf("control unexpectedly succeeded — WriteTimeout not enforced, test is invalid")
	}
	t.Logf("control truncated as expected (body=%q err=%v)", ctrl, ctrlErr)
}
