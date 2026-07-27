package scaryhttp

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

// errFake is returned by the capability stubs that only exist to make a fake
// writer satisfy an interface.
var errFake = errors.New("fake")

// capBase is a minimal http.ResponseWriter with no optional capabilities.
type capBase struct {
	hdr    http.Header
	status int
	body   bytes.Buffer
}

func newCapBase() *capBase { return &capBase{hdr: http.Header{}} }

func (c *capBase) Header() http.Header         { return c.hdr }
func (c *capBase) WriteHeader(code int)        { c.status = code }
func (c *capBase) Write(b []byte) (int, error) { return c.body.Write(b) }

// One tiny stub per optional interface. Embedding them in the fakes below
// produces every combination of capabilities without repeating method bodies.
type capF struct{}

func (capF) Flush() {}

type capH struct{}

func (capH) Hijack() (net.Conn, *bufio.ReadWriter, error) { return nil, nil, errFake }

type capR struct{}

func (capR) ReadFrom(io.Reader) (int64, error) { return 0, errFake }

type capP struct{}

func (capP) Push(string, *http.PushOptions) error { return errFake }

type fake0 struct{ *capBase }

type fakeF struct {
	*capBase
	capF
}

type fakeH struct {
	*capBase
	capH
}

type fakeFH struct {
	*capBase
	capF
	capH
}

type fakeR struct {
	*capBase
	capR
}

type fakeFR struct {
	*capBase
	capF
	capR
}

type fakeHR struct {
	*capBase
	capH
	capR
}

type fakeFHR struct {
	*capBase
	capF
	capH
	capR
}

type fakeP struct {
	*capBase
	capP
}

type fakeFP struct {
	*capBase
	capF
	capP
}

type fakeHP struct {
	*capBase
	capH
	capP
}

type fakeFHP struct {
	*capBase
	capF
	capH
	capP
}

type fakeRP struct {
	*capBase
	capR
	capP
}

type fakeFRP struct {
	*capBase
	capF
	capR
	capP
}

type fakeHRP struct {
	*capBase
	capH
	capR
	capP
}

type fakeFHRP struct {
	*capBase
	capF
	capH
	capR
	capP
}

// TestWrapWriterMirrorsInterfaces is the core guarantee of the wrapper: for
// every combination of optional interfaces, the writer handed to the handler
// implements exactly the same set as the writer it wraps.
//
// Both directions matter. Dropping http.Hijacker is what breaks WebSocket
// upgrades (gorilla/websocket type-asserts it directly). Adding one that isn't
// really there is just as wrong: code that picks between an upgrade and a
// long-polling fallback by testing for http.Hijacker would take the wrong
// branch and fail later, and echo's (*Response).Hijack asserts unchecked.
func TestWrapWriterMirrorsInterfaces(t *testing.T) {
	base := newCapBase()

	cases := []struct {
		name                       string
		w                          http.ResponseWriter
		wantF, wantH, wantR, wantP bool
	}{
		{"none", fake0{base}, false, false, false, false},
		{"F", fakeF{capBase: base}, true, false, false, false},
		{"H", fakeH{capBase: base}, false, true, false, false},
		{"FH", fakeFH{capBase: base}, true, true, false, false},
		{"R", fakeR{capBase: base}, false, false, true, false},
		{"FR", fakeFR{capBase: base}, true, false, true, false},
		{"HR", fakeHR{capBase: base}, false, true, true, false},
		{"FHR", fakeFHR{capBase: base}, true, true, true, false},
		{"P", fakeP{capBase: base}, false, false, false, true},
		{"FP", fakeFP{capBase: base}, true, false, false, true},
		{"HP", fakeHP{capBase: base}, false, true, false, true},
		{"FHP", fakeFHP{capBase: base}, true, true, false, true},
		{"RP", fakeRP{capBase: base}, false, false, true, true},
		{"FRP", fakeFRP{capBase: base}, true, false, true, true},
		{"HRP", fakeHRP{capBase: base}, false, true, true, true},
		{"FHRP", fakeFHRP{capBase: base}, true, true, true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Guard against a fake that doesn't model what the case claims.
			assertCaps(t, "underlying", tc.w, tc.wantF, tc.wantH, tc.wantR, tc.wantP)

			_, got := wrapWriter(tc.w)
			assertCaps(t, "wrapped", got, tc.wantF, tc.wantH, tc.wantR, tc.wantP)

			if u, ok := got.(interface {
				Unwrap() http.ResponseWriter
			}); !ok {
				t.Errorf("wrapped writer does not implement Unwrap() http.ResponseWriter")
			} else if u.Unwrap() != tc.w {
				t.Errorf("Unwrap() returned %T, want the original %T", u.Unwrap(), tc.w)
			}
		})
	}
}

func assertCaps(t *testing.T, what string, w http.ResponseWriter, wantF, wantH, wantR, wantP bool) {
	t.Helper()
	if _, ok := w.(http.Flusher); ok != wantF {
		t.Errorf("%s: http.Flusher = %v, want %v", what, ok, wantF)
	}
	if _, ok := w.(http.Hijacker); ok != wantH {
		t.Errorf("%s: http.Hijacker = %v, want %v", what, ok, wantH)
	}
	if _, ok := w.(io.ReaderFrom); ok != wantR {
		t.Errorf("%s: io.ReaderFrom = %v, want %v", what, ok, wantR)
	}
	if _, ok := w.(http.Pusher); ok != wantP {
		t.Errorf("%s: http.Pusher = %v, want %v", what, ok, wantP)
	}
}

// capableWriter implements every optional interface and records what was called,
// so the delegation tests can assert the call reached the wrapped writer.
type capableWriter struct {
	*capBase
	flushed  bool
	pushed   string
	readFrom int64
	conn     net.Conn
}

func (c *capableWriter) Flush() { c.flushed = true }

func (c *capableWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return c.conn, bufio.NewReadWriter(bufio.NewReader(c.conn), bufio.NewWriter(c.conn)), nil
}

func (c *capableWriter) ReadFrom(src io.Reader) (int64, error) {
	n, err := io.Copy(&c.body, src)
	c.readFrom = n
	return n, err
}

func (c *capableWriter) Push(target string, _ *http.PushOptions) error {
	c.pushed = target
	return nil
}

func newCapableWriter(t *testing.T) *capableWriter {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	return &capableWriter{capBase: newCapBase(), conn: server}
}

func TestRecorderDelegatesToWrappedWriter(t *testing.T) {
	t.Run("flush", func(t *testing.T) {
		under := newCapableWriter(t)
		_, w := wrapWriter(under)
		w.(http.Flusher).Flush()
		if !under.flushed {
			t.Errorf("Flush did not reach the wrapped writer")
		}
	})

	t.Run("hijack", func(t *testing.T) {
		under := newCapableWriter(t)
		rec, w := wrapWriter(under)

		conn, brw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Fatalf("Hijack: %v", err)
		}
		if conn != under.conn || brw == nil {
			t.Errorf("Hijack returned conn %v, want the wrapped writer's %v", conn, under.conn)
		}
		if !rec.hijacked {
			t.Errorf("recorder did not mark the connection as hijacked")
		}
		if rec.status != http.StatusSwitchingProtocols {
			t.Errorf("status after hijack = %d, want %d", rec.status, http.StatusSwitchingProtocols)
		}
	})

	t.Run("readFrom", func(t *testing.T) {
		under := newCapableWriter(t)
		rec, w := wrapWriter(under)

		n, err := w.(io.ReaderFrom).ReadFrom(strings.NewReader("hello world"))
		if err != nil {
			t.Fatalf("ReadFrom: %v", err)
		}
		if n != 11 || under.readFrom != 11 {
			t.Errorf("ReadFrom n = %d, wrapped saw %d, want 11 for both", n, under.readFrom)
		}
		if rec.written != 11 {
			t.Errorf("recorder.written = %d, want 11", rec.written)
		}
	})

	t.Run("push", func(t *testing.T) {
		under := newCapableWriter(t)
		_, w := wrapWriter(under)
		if err := w.(http.Pusher).Push("/style.css", nil); err != nil {
			t.Fatalf("Push: %v", err)
		}
		if under.pushed != "/style.css" {
			t.Errorf("Push target reached wrapped writer as %q, want /style.css", under.pushed)
		}
	})
}

// A writer without the capability must report it as unsupported rather than
// pretend, if the delegate is ever reached.
func TestRecorderUnsupportedCapabilities(t *testing.T) {
	rec, _ := wrapWriter(fake0{newCapBase()})

	if _, _, err := rec.hijack(); !errors.Is(err, http.ErrNotSupported) {
		t.Errorf("hijack on a non-Hijacker: err = %v, want http.ErrNotSupported", err)
	}
	if err := rec.push("/x", nil); !errors.Is(err, http.ErrNotSupported) {
		t.Errorf("push on a non-Pusher: err = %v, want http.ErrNotSupported", err)
	}
	rec.flush() // must not panic
}

func TestRecorderCapturesStatusAndBytes(t *testing.T) {
	under := newCapBase()
	rec, w := wrapWriter(fake0{under})

	w.WriteHeader(http.StatusTeapot)
	w.WriteHeader(http.StatusOK) // a second call must not overwrite the first
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if rec.status != http.StatusTeapot {
		t.Errorf("status = %d, want %d", rec.status, http.StatusTeapot)
	}
	if rec.written != 5 {
		t.Errorf("written = %d, want 5", rec.written)
	}
	if under.body.String() != "hello" {
		t.Errorf("wrapped writer got %q, want %q", under.body.String(), "hello")
	}
}
