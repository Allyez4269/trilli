// Package httpx holds small HTTP helpers shared across the handler packages.
//
// Its first job is streaming deadlines. The server applies a single absolute
// ReadTimeout/WriteTimeout to every connection (good slow-loris protection for
// ordinary JSON requests), but that same blanket deadline would truncate any
// large file transfer — or a slow document conversion — that runs longer than
// the window. The wrappers here convert that absolute deadline into an
// INACTIVITY deadline on the streaming routes: a steady (even slow) transfer
// runs as long as it needs, while a genuinely stalled peer is still dropped.
package httpx

import (
	"io"
	"net/http"
	"time"
)

// DefaultTransferIdleTimeout is the inactivity window used for large file
// up/downloads and preview streaming. A transfer is only abandoned after this
// long with NO progress (not total time) — so a steadily-arriving 10GB upload is
// fine; only a genuine stall trips it. 60s was too aggressive: a multi-hour
// upload over a slow/flaky link hits transient >60s stalls and gets killed
// (surfaces to the client as a 400 "invalid multipart body"). 5 minutes rides
// out real network hiccups while still reclaiming a truly dead connection.
const DefaultTransferIdleTimeout = 5 * time.Minute

// IdleWriter pushes the connection's write deadline to now+idle before every
// write, turning the server's absolute WriteTimeout into an inactivity timeout
// for a streamed response. If the underlying ResponseWriter can't expose a
// deadline (e.g. wrapped without Unwrap), it stops trying and writes pass
// through under the server default.
type IdleWriter struct {
	w    io.Writer
	rc   *http.ResponseController
	idle time.Duration
	set  bool
}

// NewIdleWriter wraps w so each write extends the write deadline by idle.
func NewIdleWriter(w http.ResponseWriter, idle time.Duration) *IdleWriter {
	return &IdleWriter{w: w, rc: http.NewResponseController(w), idle: idle, set: true}
}

func (iw *IdleWriter) Write(p []byte) (int, error) {
	if iw.set {
		if err := iw.rc.SetWriteDeadline(time.Now().Add(iw.idle)); err != nil {
			iw.set = false // deadlines unsupported here — fall back to server default
		}
	}
	return iw.w.Write(p)
}

// IdleReader is IdleWriter's read-side twin for large request bodies (uploads):
// each Read pushes the read deadline out, so a big upload isn't cut at the
// server's absolute ReadTimeout while bytes keep arriving.
type IdleReader struct {
	r    io.ReadCloser
	rc   *http.ResponseController
	idle time.Duration
	set  bool
}

// NewIdleReader wraps a request body so each read extends the read deadline by
// idle. Assign the result back to r.Body before consuming the body.
func NewIdleReader(w http.ResponseWriter, body io.ReadCloser, idle time.Duration) *IdleReader {
	return &IdleReader{r: body, rc: http.NewResponseController(w), idle: idle, set: true}
}

func (ir *IdleReader) Read(p []byte) (int, error) {
	if ir.set {
		if err := ir.rc.SetReadDeadline(time.Now().Add(ir.idle)); err != nil {
			ir.set = false
		}
	}
	return ir.r.Read(p)
}

func (ir *IdleReader) Close() error { return ir.r.Close() }
