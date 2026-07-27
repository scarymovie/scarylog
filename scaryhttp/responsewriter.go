package scaryhttp

import (
	"bufio"
	"io"
	"net"
	"net/http"
)

// recorder wraps an http.ResponseWriter to capture the status code and the
// number of bytes written for the finish log line.
//
// It deliberately does NOT declare Flush/Hijack/ReadFrom/Push itself. Those are
// exposed by the wrapper types below, and only for the interfaces the wrapped
// writer actually implements — see wrapWriter.
type recorder struct {
	http.ResponseWriter
	status      int
	written     int64
	wroteHeader bool
	hijacked    bool
}

func (r *recorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.wroteHeader = true
	}
	n, err := r.ResponseWriter.Write(b)
	r.written += int64(n)
	return n, err
}

// Unwrap exposes the wrapped writer so http.ResponseController (Go 1.20+) can
// reach it. This is additional to the explicit passthroughs, not a replacement:
// libraries such as gorilla/websocket type-assert the writer directly and never
// consult Unwrap.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// The delegates below are unexported on purpose: an unexported method does not
// satisfy http.Flusher and friends, so embedding *recorder never leaks a
// capability the wrapped writer does not have.

func (r *recorder) flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		r.wroteHeader = true
		f.Flush()
	}
}

func (r *recorder) hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	conn, brw, err := h.Hijack()
	if err == nil {
		r.hijacked = true
		if !r.wroteHeader {
			// The handshake response is written straight to the connection, so
			// record what the client actually saw.
			r.status = http.StatusSwitchingProtocols
			r.wroteHeader = true
		}
	}
	return conn, brw, err
}

func (r *recorder) readFrom(src io.Reader) (int64, error) {
	rf, ok := r.ResponseWriter.(io.ReaderFrom)
	if !ok {
		// Unreachable: ReadFrom is only exposed when the writer implements it.
		// io.Copy goes through r.Write, which already counts the bytes.
		return io.Copy(r, src)
	}
	r.wroteHeader = true
	n, err := rf.ReadFrom(src)
	r.written += n
	return n, err
}

func (r *recorder) push(target string, opts *http.PushOptions) error {
	p, ok := r.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return p.Push(target, opts)
}

// Optional interfaces the wrapped writer may implement.
const (
	flusherBit = 1 << iota
	hijackerBit
	readerFromBit
	pusherBit
)

// wrapWriter returns the recorder holding the captured state and the writer to
// hand to the next handler. The returned writer implements exactly the optional
// interfaces w implements — no more, no less — so a type assertion behaves the
// same with and without this middleware in the chain. That matters: code that
// picks between a WebSocket upgrade and a long-polling fallback by testing for
// http.Hijacker must not be told "yes" on a connection that cannot be hijacked.
func wrapWriter(w http.ResponseWriter) (*recorder, http.ResponseWriter) {
	rec := &recorder{ResponseWriter: w, status: http.StatusOK}

	var bits int
	if _, ok := w.(http.Flusher); ok {
		bits |= flusherBit
	}
	if _, ok := w.(http.Hijacker); ok {
		bits |= hijackerBit
	}
	if _, ok := w.(io.ReaderFrom); ok {
		bits |= readerFromBit
	}
	if _, ok := w.(http.Pusher); ok {
		bits |= pusherBit
	}

	switch bits {
	case 0:
		return rec, rw{rec}
	case flusherBit:
		return rec, rwF{rec}
	case hijackerBit:
		return rec, rwH{rec}
	case flusherBit | hijackerBit:
		return rec, rwFH{rec}
	case readerFromBit:
		return rec, rwR{rec}
	case flusherBit | readerFromBit:
		return rec, rwFR{rec}
	case hijackerBit | readerFromBit:
		return rec, rwHR{rec}
	case flusherBit | hijackerBit | readerFromBit:
		return rec, rwFHR{rec}
	case pusherBit:
		return rec, rwP{rec}
	case flusherBit | pusherBit:
		return rec, rwFP{rec}
	case hijackerBit | pusherBit:
		return rec, rwHP{rec}
	case flusherBit | hijackerBit | pusherBit:
		return rec, rwFHP{rec}
	case readerFromBit | pusherBit:
		return rec, rwRP{rec}
	case flusherBit | readerFromBit | pusherBit:
		return rec, rwFRP{rec}
	case hijackerBit | readerFromBit | pusherBit:
		return rec, rwHRP{rec}
	default: // flusherBit | hijackerBit | readerFromBit | pusherBit
		return rec, rwFHRP{rec}
	}
}

// One wrapper type per combination of optional interfaces. Each embeds
// *recorder for the http.ResponseWriter methods and Unwrap, and declares only
// the exported methods matching its own name: F = http.Flusher,
// H = http.Hijacker, R = io.ReaderFrom, P = http.Pusher.

type rw struct{ *recorder }

type rwF struct{ *recorder }

func (w rwF) Flush() { w.flush() }

type rwH struct{ *recorder }

func (w rwH) Hijack() (net.Conn, *bufio.ReadWriter, error) { return w.hijack() }

type rwFH struct{ *recorder }

func (w rwFH) Flush()                                       { w.flush() }
func (w rwFH) Hijack() (net.Conn, *bufio.ReadWriter, error) { return w.hijack() }

type rwR struct{ *recorder }

func (w rwR) ReadFrom(src io.Reader) (int64, error) { return w.readFrom(src) }

type rwFR struct{ *recorder }

func (w rwFR) Flush()                                { w.flush() }
func (w rwFR) ReadFrom(src io.Reader) (int64, error) { return w.readFrom(src) }

type rwHR struct{ *recorder }

func (w rwHR) Hijack() (net.Conn, *bufio.ReadWriter, error) { return w.hijack() }
func (w rwHR) ReadFrom(src io.Reader) (int64, error)        { return w.readFrom(src) }

type rwFHR struct{ *recorder }

func (w rwFHR) Flush()                                       { w.flush() }
func (w rwFHR) Hijack() (net.Conn, *bufio.ReadWriter, error) { return w.hijack() }
func (w rwFHR) ReadFrom(src io.Reader) (int64, error)        { return w.readFrom(src) }

type rwP struct{ *recorder }

func (w rwP) Push(target string, opts *http.PushOptions) error { return w.push(target, opts) }

type rwFP struct{ *recorder }

func (w rwFP) Flush()                                           { w.flush() }
func (w rwFP) Push(target string, opts *http.PushOptions) error { return w.push(target, opts) }

type rwHP struct{ *recorder }

func (w rwHP) Hijack() (net.Conn, *bufio.ReadWriter, error)     { return w.hijack() }
func (w rwHP) Push(target string, opts *http.PushOptions) error { return w.push(target, opts) }

type rwFHP struct{ *recorder }

func (w rwFHP) Flush()                                           { w.flush() }
func (w rwFHP) Hijack() (net.Conn, *bufio.ReadWriter, error)     { return w.hijack() }
func (w rwFHP) Push(target string, opts *http.PushOptions) error { return w.push(target, opts) }

type rwRP struct{ *recorder }

func (w rwRP) ReadFrom(src io.Reader) (int64, error)            { return w.readFrom(src) }
func (w rwRP) Push(target string, opts *http.PushOptions) error { return w.push(target, opts) }

type rwFRP struct{ *recorder }

func (w rwFRP) Flush()                                           { w.flush() }
func (w rwFRP) ReadFrom(src io.Reader) (int64, error)            { return w.readFrom(src) }
func (w rwFRP) Push(target string, opts *http.PushOptions) error { return w.push(target, opts) }

type rwHRP struct{ *recorder }

func (w rwHRP) Hijack() (net.Conn, *bufio.ReadWriter, error)     { return w.hijack() }
func (w rwHRP) ReadFrom(src io.Reader) (int64, error)            { return w.readFrom(src) }
func (w rwHRP) Push(target string, opts *http.PushOptions) error { return w.push(target, opts) }

type rwFHRP struct{ *recorder }

func (w rwFHRP) Flush()                                           { w.flush() }
func (w rwFHRP) Hijack() (net.Conn, *bufio.ReadWriter, error)     { return w.hijack() }
func (w rwFHRP) ReadFrom(src io.Reader) (int64, error)            { return w.readFrom(src) }
func (w rwFHRP) Push(target string, opts *http.PushOptions) error { return w.push(target, opts) }
