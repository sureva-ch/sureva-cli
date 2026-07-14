package authflow

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// DefaultPorts are the loopback ports tried in order (see docs/cognito-cli-client.md in the cloud-api repo).
var DefaultPorts = []int{8976, 8977, 8978}

// DefaultTimeout bounds how long the server waits for the callback.
const DefaultTimeout = 5 * time.Minute

// shutdownGrace bounds the wait for an in-flight response before closing.
const shutdownGrace = 2 * time.Second

// CallbackResult holds the authorization code from a validated callback.
type CallbackResult struct {
	Code string
}

// PortsBusyError means no port could be bound; callers MUST NOT open a browser.
type PortsBusyError struct {
	Ports []int
}

func (e *PortsBusyError) Error() string {
	return fmt.Sprintf("all loopback callback ports are busy: %v", e.Ports)
}

// StateMismatchError is a CSRF defense: callers MUST NOT exchange the code.
type StateMismatchError struct{}

func (*StateMismatchError) Error() string {
	return "callback state does not match the value generated for this login attempt"
}

// IdPError wraps the OAuth `error`/`error_description` query parameters.
type IdPError struct {
	Code        string
	Description string
}

func (e *IdPError) Error() string {
	// error_description is untrusted identity-provider text and could contain
	// reflected query values. Do not echo either callback parameter.
	return "identity provider did not complete authorization"
}

// CallbackError means the callback had neither a code nor an error param.
type CallbackError struct {
	Reason string
}

func (e *CallbackError) Error() string {
	return fmt.Sprintf("invalid OAuth callback: %s", e.Reason)
}

// TimeoutError means no callback arrived before ctx was done.
type TimeoutError struct{}

func (*TimeoutError) Error() string {
	return "timed out waiting for the browser login callback"
}

// Listener is a bound loopback listener; WaitForCallback serves one redirect.
type Listener struct {
	port int
	ln   net.Listener
}

// Listen binds 127.0.0.1 (never 0.0.0.0), trying each port in order.
func Listen(ports []int) (*Listener, error) {
	for _, port := range ports {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			return &Listener{port: port, ln: ln}, nil
		}
	}
	return nil, &PortsBusyError{Ports: ports}
}

// Port returns the bound loopback port.
func (l *Listener) Port() int { return l.port }

// Addr returns the bound network address.
func (l *Listener) Addr() net.Addr { return l.ln.Addr() }

// Close releases the listener; safe after WaitForCallback already shut it down.
func (l *Listener) Close() error { return l.ln.Close() }

// callbackOutcome carries the terminal result of the callback wait.
type callbackOutcome struct {
	result *CallbackResult
	err    error
}

// callbackHandler returns the single-use /callback handler. The first request
// wins the sync.Once and produces the flow outcome; every later request gets
// the 410 Gone one-shot page. Extracted from WaitForCallback so the 410
// branch is unit-testable without racing real TCP connections.
func callbackHandler(expectedState string, once *sync.Once, outcomeCh chan<- callbackOutcome) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handled := false
		once.Do(func() { handled = true })
		if !handled {
			writePage(w, http.StatusGone, "Login link already used", "You can close this tab and return to the terminal.")
			return
		}
		result, err := parseCallback(r, expectedState)
		writeCallbackPage(w, err)
		outcomeCh <- callbackOutcome{result: result, err: err}
	}
}

// WaitForCallback serves exactly one GET /callback, validating state, then
// shuts down (first request processed, or ctx done, whichever is first).
func (l *Listener) WaitForCallback(ctx context.Context, expectedState string) (*CallbackResult, error) {
	outcomeCh := make(chan callbackOutcome, 1)
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", callbackHandler(expectedState, &once, outcomeCh))

	httpSrv := &http.Server{Handler: mux}
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- httpSrv.Serve(l.ln) }()
	var out callbackOutcome
	select {
	case out = <-outcomeCh:
	case <-ctx.Done():
		out = callbackOutcome{err: &TimeoutError{}}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	<-serveErrCh
	return out.result, out.err
}

// parseCallback checks state (CSRF), then IdP error, then requires a code.
func parseCallback(r *http.Request, expectedState string) (*CallbackResult, error) {
	if r.Method != http.MethodGet {
		return nil, &CallbackError{Reason: "unsupported callback method"}
	}
	q := r.URL.Query()

	if q.Get("state") != expectedState {
		return nil, &StateMismatchError{}
	}
	if errCode := q.Get("error"); errCode != "" {
		return nil, &IdPError{Code: errCode, Description: q.Get("error_description")}
	}
	code := q.Get("code")
	if code == "" {
		return nil, &CallbackError{Reason: "missing authorization code"}
	}
	return &CallbackResult{Code: code}, nil
}

// writeCallbackPage renders success or error HTML; never echoes raw code/state.
func writeCallbackPage(w http.ResponseWriter, err error) {
	if err == nil {
		writePage(w, http.StatusOK, "Login successful", "You can close this tab and return to the terminal.")
		return
	}

	reason := "The login attempt could not be completed."
	switch err.(type) {
	case *StateMismatchError:
		reason = "The login attempt could not be verified. Please run `sureva login` again."
	case *IdPError:
		reason = "Authorization was not completed."
	case *CallbackError:
		reason = "The login callback was invalid."
	}
	writePage(w, http.StatusOK, "Login failed", reason+" You can close this tab and return to the terminal.")
}

// writePage renders a minimal, secret-free HTML page.
func writePage(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<!DOCTYPE html><html lang=\"en\"><head><meta charset=\"utf-8\"><title>%s</title></head>"+
		"<body><h1>%s</h1><p>%s</p></body></html>\n", title, title, message)
}
