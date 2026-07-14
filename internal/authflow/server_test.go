package authflow

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// freePort returns an OS-assigned free port on 127.0.0.1.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("freePort: close: %v", err)
	}
	return port
}

// occupy binds port so it looks busy to Listen; the caller's cleanup closes it.
func occupy(t *testing.T, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("occupy port %d: %v", port, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
}

// mustListen binds ports or fails the test, closing the listener on cleanup.
func mustListen(t *testing.T, ports []int) *Listener {
	t.Helper()
	l, err := Listen(ports)
	if err != nil {
		t.Fatalf("Listen() error: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// waitAsync runs WaitForCallback in a goroutine so callers send it concurrently.
func waitAsync(ctx context.Context, l *Listener, state string) <-chan callbackOutcome {
	ch := make(chan callbackOutcome, 1)
	go func() {
		r, err := l.WaitForCallback(ctx, state)
		ch <- callbackOutcome{r, err}
	}()
	return ch
}

// TestListen covers loopback-only binding, port fallback, and all-busy.
func TestListen(t *testing.T) {
	t.Run("binds loopback only, never wildcard", func(t *testing.T) {
		port := freePort(t)
		l := mustListen(t, []int{port})
		tcpAddr, ok := l.Addr().(*net.TCPAddr)
		if !ok || !tcpAddr.IP.IsLoopback() || tcpAddr.IP.IsUnspecified() || tcpAddr.IP.String() != "127.0.0.1" {
			t.Errorf("bound addr = %v, want exactly 127.0.0.1 (never 0.0.0.0)", l.Addr())
		}
		if l.Port() != port {
			t.Errorf("Port() = %d, want %d", l.Port(), port)
		}
	})

	t.Run("falls back to next port when first is busy", func(t *testing.T) {
		busy := freePort(t)
		occupy(t, busy)
		free := freePort(t)
		l := mustListen(t, []int{busy, free})
		if l.Port() != free {
			t.Errorf("Port() = %d, want fallback port %d", l.Port(), free)
		}
	})

	t.Run("all ports busy errors before any browser call", func(t *testing.T) {
		p1, p2 := freePort(t), freePort(t)
		occupy(t, p1)
		occupy(t, p2)
		l, err := Listen([]int{p1, p2})
		if l != nil {
			t.Errorf("Listen() = %v, want nil on all-busy", l)
		}
		busyErr, ok := err.(*PortsBusyError)
		if !ok {
			t.Fatalf("err = %v (%T), want *PortsBusyError", err, err)
		}
		if len(busyErr.Ports) != 2 || busyErr.Ports[0] != p1 || busyErr.Ports[1] != p2 {
			t.Errorf("PortsBusyError.Ports = %v, want [%d %d]", busyErr.Ports, p1, p2)
		}
	})
}

// TestListener_WaitForCallback_Scenarios covers success, state mismatch
// (CSRF), IdP denial, and a malformed callback.
func TestListener_WaitForCallback_Scenarios(t *testing.T) {
	tests := []struct {
		name           string
		expectedState  string
		query          string
		wantCode       string
		wantErrType    string // "" | "state" | "idp" | "callback"
		wantIdPCode    string
		assertShutdown bool // second GET after success must be refused (single-use)
	}{
		{
			name:           "valid code and matching state",
			expectedState:  "expected-state",
			query:          "code=abc123&state=expected-state",
			wantCode:       "abc123",
			assertShutdown: true,
		},
		{
			name:          "state mismatch is rejected",
			expectedState: "expected-state",
			query:         "code=super-secret-code&state=wrong-state",
			wantErrType:   "state",
		},
		{
			name:          "IdP denial is detected before any exchange",
			expectedState: "expected-state",
			query:         "error=access_denied&error_description=User+denied+access&state=expected-state",
			wantErrType:   "idp",
			wantIdPCode:   "access_denied",
		},
		{
			name:          "missing code with matching state is a callback error",
			expectedState: "expected-state",
			query:         "state=expected-state",
			wantErrType:   "callback",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port := freePort(t)
			l := mustListen(t, []int{port})
			resultCh := waitAsync(context.Background(), l, tt.expectedState)
			resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?%s", port, tt.query))
			if err != nil {
				t.Fatalf("GET /callback: %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			out := <-resultCh
			bodyStr := string(body)
			if !strings.Contains(bodyStr, "close this tab") {
				t.Errorf("response body does not contain close-tab guidance: %q", bodyStr)
			}
			if strings.Contains(bodyStr, "super-secret-code") || strings.Contains(bodyStr, "wrong-state") {
				t.Error("response body leaked a secret query value (code/state)")
			}
			switch tt.wantErrType {
			case "":
				if out.err != nil {
					t.Fatalf("unexpected error: %v", out.err)
				}
				if out.result == nil || out.result.Code != tt.wantCode {
					t.Errorf("result = %+v, want Code %q", out.result, tt.wantCode)
				}
			case "state":
				if _, ok := out.err.(*StateMismatchError); !ok {
					t.Fatalf("err = %v (%T), want *StateMismatchError", out.err, out.err)
				}
				if out.result != nil {
					t.Errorf("result = %+v, want nil on state mismatch", out.result)
				}
			case "idp":
				idpErr, ok := out.err.(*IdPError)
				if !ok {
					t.Fatalf("err = %v (%T), want *IdPError", out.err, out.err)
				}
				if idpErr.Code != tt.wantIdPCode {
					t.Errorf("IdPError.Code = %q, want %q", idpErr.Code, tt.wantIdPCode)
				}
				if out.result != nil {
					t.Errorf("result = %+v, want nil on IdP denial", out.result)
				}
				// error_description is a free-text field from the identity
				// provider; it must never be reflected into the HTML page
				// (only the error code is, and it's HTML-escaped).
				if strings.Contains(bodyStr, "User denied access") {
					t.Error("response body reflected error_description content, want it never rendered")
				}
			case "callback":
				if _, ok := out.err.(*CallbackError); !ok {
					t.Fatalf("err = %v (%T), want *CallbackError", out.err, out.err)
				}
			}
			// assertShutdown: post-return, the port must refuse a second GET.
			if tt.assertShutdown {
				if _, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?code=second&state=%s", port, tt.expectedState)); err == nil {
					t.Error("second request after shutdown succeeded, want connection error")
				}
			}
		})
	}
}

// dialAndSend opens a raw TCP connection and writes a full HTTP/1.1 request
// immediately, before the server has started Serve()'ing. The OS accepts
// the handshake and buffers the written bytes at the kernel level even
// though the application hasn't called Accept() yet, so by the time Serve
// starts, both requests are already available to read — making the "two
// callbacks racing the single-use handler" scenario deterministic instead
// of a timing-dependent goroutine race.
func dialAndSend(t *testing.T, addr, path string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", path, addr)
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write request: %v", err)
	}
	return conn
}

// tryReadRawResponse reads one HTTP response off a raw connection
// established by dialAndSend, returning an error instead of failing the
// test — a dropped/reset connection here is a goroutine-scheduling
// artifact of the race harness, not a server logic bug (see
// TestListener_WaitForCallback_ConcurrentSecondCallback).
func tryReadRawResponse(conn net.Conn) (status int, body string, err error) {
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return 0, "", err
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, string(b), nil
}

// TestCallbackHandler_SecondRequestGetsGone deterministically pins the 410
// one-shot branch: the first request consumes the sync.Once, the second gets
// the Gone page. No TCP race involved, so this cannot flake on scheduling.
func TestCallbackHandler_SecondRequestGetsGone(t *testing.T) {
	var once sync.Once
	outcomeCh := make(chan callbackOutcome, 1)
	h := callbackHandler("expected-state", &once, outcomeCh)

	rec1 := httptest.NewRecorder()
	h(rec1, httptest.NewRequest(http.MethodGet, "/callback?code=code-one&state=expected-state", nil))
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want %d", rec1.Code, http.StatusOK)
	}

	rec2 := httptest.NewRecorder()
	h(rec2, httptest.NewRequest(http.MethodGet, "/callback?code=code-two&state=expected-state", nil))
	if rec2.Code != http.StatusGone {
		t.Fatalf("second request status = %d, want %d", rec2.Code, http.StatusGone)
	}
	if !strings.Contains(rec2.Body.String(), "already used") {
		t.Fatalf("410 body = %q, want the one-shot 'already used' page", rec2.Body.String())
	}
	for _, rec := range []*httptest.ResponseRecorder{rec1, rec2} {
		if strings.Contains(rec.Body.String(), "code-one") || strings.Contains(rec.Body.String(), "code-two") {
			t.Fatal("response body leaked a secret code value")
		}
	}
}

// TestListener_WaitForCallback_ConcurrentSecondCallback proves that when two
// callbacks race for the single-use handler, exactly one is processed and no
// secret leaks — regardless of scheduling. The losing request legitimately
// sees either the 410 Gone page (it reached the handler before shutdown) or a
// dropped connection (Shutdown reaped it first); both preserve the one-shot
// invariant. The 410 page itself is pinned deterministically by
// TestCallbackHandler_SecondRequestGetsGone.
func TestListener_WaitForCallback_ConcurrentSecondCallback(t *testing.T) {
	port := freePort(t)
	l := mustListen(t, []int{port})
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	conn1 := dialAndSend(t, addr, "/callback?code=code-one&state=expected-state")
	defer func() { _ = conn1.Close() }()
	conn2 := dialAndSend(t, addr, "/callback?code=code-two&state=expected-state")
	defer func() { _ = conn2.Close() }()
	// Give the kernel time to fully buffer both requests before Serve starts
	// accepting, so both land in the race window.
	time.Sleep(20 * time.Millisecond)

	resultCh := waitAsync(context.Background(), l, "expected-state")

	responses := []struct {
		status int
		body   string
		err    error
	}{}
	for _, conn := range []net.Conn{conn1, conn2} {
		status, body, err := tryReadRawResponse(conn)
		responses = append(responses, struct {
			status int
			body   string
			err    error
		}{status, body, err})
	}
	out := <-resultCh
	if out.err != nil {
		t.Fatalf("WaitForCallback error: %v", out.err)
	}

	ok, gone, dropped := 0, 0, 0
	for _, r := range responses {
		switch {
		case r.err != nil:
			// Shutdown reaped the losing connection before its response was
			// written — a legitimate outcome of the race, the one-shot
			// invariant still holds.
			dropped++
			continue
		case r.status == http.StatusGone:
			gone++
			if !strings.Contains(r.body, "already used") {
				t.Fatalf("410 body = %q, want the one-shot 'already used' page", r.body)
			}
		default:
			ok++
		}
		if strings.Contains(r.body, "code-one") || strings.Contains(r.body, "code-two") {
			t.Fatal("response body leaked a secret code value")
		}
	}
	if ok != 1 || gone+dropped != 1 {
		t.Fatalf("ok=%d gone=%d dropped=%d, want exactly one processed response and one refused loser", ok, gone, dropped)
	}
}

// TestListener_WaitForCallback_Timeout proves shutdown when ctx is done.
func TestListener_WaitForCallback_Timeout(t *testing.T) {
	port := freePort(t)
	l := mustListen(t, []int{port})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := l.WaitForCallback(ctx, "expected-state")
	if _, ok := err.(*TimeoutError); !ok {
		t.Fatalf("err = %v (%T), want *TimeoutError", err, err)
	}

	if _, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback", port)); err == nil {
		t.Error("request after timeout shutdown succeeded, want connection error")
	}
}
