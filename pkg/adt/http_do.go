package adt

import (
	"net/http"
	"net/url"
	"strings"
)

// stripContextID removes a non-empty sap-contextid cookie from req.
func stripContextID(req *http.Request) {
	cookies := req.Cookies()
	kept := cookies[:0]
	stripped := false
	for _, c := range cookies {
		if c.Name == "sap-contextid" && c.Value != "" {
			stripped = true
			continue
		}
		kept = append(kept, c)
	}
	if !stripped {
		return
	}
	req.Header.Del("Cookie")
	for _, c := range kept {
		req.AddCookie(c)
	}
}

// isolating reports whether a stateless request would now be kept out of the
// stateful context: a lock handle is outstanding, or a request into the
// context is under way or waiting.
func (t *Transport) isolating() bool {
	return t.contextInFlight.Load() > 0 || (t.locks != nil && t.locks.present())
}

// isolatingNow is isolating for the session-error recovery: a lock record
// nobody closed ages out after the keep-alive limit instead of twelve hours, so
// a lost handle cannot disable recovery for a working day.
func (t *Transport) isolatingNow() bool {
	return t.contextInFlight.Load() > 0 || (t.locks != nil && t.locks.outstanding())
}

// do sends a request, keeping concurrent callers of one client from breaking
// each other's lock chains (upstream #251, issue #169). Two things end or
// break the stateful ADT context a lock handle is bound to, and both are the
// ordinary work of another caller sharing this client:
//
//   - A stateless request ends the context it arrives in, and the cookie jar
//     puts the context's sap-contextid on every request. One sent between LOCK
//     and the write turns the write into 423. So while a lock is outstanding,
//     or a stateful request is under way, a stateless request goes without the
//     jar: it carries the session's other cookies but not sap-contextid, and
//     the cookies its response sets are not learned.
//   - The ICM serves a stateful context one request at a time and answers a
//     second concurrent one with 400; the session recovery that follows drops
//     the cookies and orphans the context together with the enqueue it holds.
//     So requests into the context -- stateful ones, and unmarked ones such as
//     the CSRF probe -- go one at a time. Chains still interleave: one context
//     holds several locks.
//
// A stateless request outside any lock window still goes into the context and
// ends it, as before -- that is how a finished chain's context is retired. It
// holds the gate shared while it does, so a LOCK cannot open a window in the
// context it is about to end; stateless requests still run side by side. A
// stateless request never waits for the gate. The gate is released when Do
// returns, not when the body is closed, so a caller that forgets to close a
// body cannot hold it forever.
//
// Requests into the context wait their turn only as long as their own context
// lasts: a long stateful request (DebuggerListen, up to its timeout) holds the
// gate for as long, and every other stateful or unmarked request on this client
// queues behind it. Before this the ICM answered those with 400 and the
// recovery orphaned the context, so waiting is the better failure.
func (t *Transport) do(req *http.Request) (*http.Response, error) {
	marker := req.Header.Get("X-sap-adt-sessiontype")
	if marker == "stateless" {
		if t.contextGate.tryShared() {
			if !t.isolating() {
				defer t.contextGate.releaseShared()
				return t.httpClient.Do(req)
			}
			t.contextGate.releaseShared()
		}
		return t.doIsolated(req)
	}

	if marker == "stateful" {
		t.contextInFlight.Add(1)
		defer t.contextInFlight.Add(-1)
	}
	if err := t.contextGate.lock(req.Context()); err != nil {
		return nil, &url.Error{Op: urlErrorOp(req.Method), URL: req.URL.String(), Err: err}
	}
	defer t.contextGate.unlock()
	return t.httpClient.Do(req)
}

// doIsolated sends a stateless request without the stateful context's id and
// without learning the cookies of the response.
func (t *Transport) doIsolated(req *http.Request) (*http.Response, error) {
	// Cookies supplied with the configuration (browser or SAML logon) were put
	// on the request already, sap-contextid among them; it would end the
	// context just the same. The empty one the proxy guard sends stays.
	stripContextID(req)
	hc, ok := t.httpClient.(*http.Client)
	t.jarMu.RLock()
	jar := t.jar
	t.jarMu.RUnlock()
	if !ok || jar == nil {
		return t.httpClient.Do(req)
	}
	for _, c := range jar.Cookies(req.URL) {
		if c.Name != "sap-contextid" {
			req.AddCookie(c)
		}
	}
	// A client of its own, built field by field: copying *hc would read its
	// Jar while clearSAPSessionCookies may be replacing it.
	isolated := &http.Client{Transport: hc.Transport, CheckRedirect: hc.CheckRedirect, Timeout: hc.Timeout}
	return isolated.Do(req)
}

// urlErrorOp names the method the way net/http does in its *url.Error.
func urlErrorOp(method string) string {
	if method == "" {
		return "Get"
	}
	return method[:1] + strings.ToLower(method[1:])
}
