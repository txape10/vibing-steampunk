package adt

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

// writtenParts remembers, for the life of the process, the sources this client
// wrote: the normalized object URI -> the URL it was written to and the hash of
// what was sent. It lets a refused activation tell the parts this process wrote
// (safe to activate with their parent) from parts someone else left pending.
// It has no expiry: it lives and dies with the process.
type writtenParts struct {
	mu sync.Mutex
	m  map[string]writtenPart
}

type writtenPart struct {
	sourceURL string
	hash      string
}

// normalizeActivationURI is the key shared by the written-parts registry and
// the inactive-object list: lower case, no query, no /source/main, namespaces
// (%2f) decoded.
func normalizeActivationURI(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	if p, err := url.PathUnescape(u); err == nil {
		u = p
	}
	return strings.TrimSuffix(u, "/source/main")
}

// noteSourceWritten records a source PUT that SAP accepted.
func (c *Client) noteSourceWritten(sourceURL, source string) {
	key := normalizeActivationURI(sourceURL)
	if key == "" {
		return
	}
	c.written.mu.Lock()
	defer c.written.mu.Unlock()
	if c.written.m == nil {
		c.written.m = map[string]writtenPart{}
	}
	c.written.m[key] = writtenPart{sourceURL: sourceURL, hash: SourceHash(source)}
}

func (c *Client) writtenPartFor(uri string) (writtenPart, bool) {
	c.written.mu.Lock()
	defer c.written.mu.Unlock()
	p, ok := c.written.m[normalizeActivationURI(uri)]
	return p, ok
}

func (c *Client) forgetWritten(uris ...string) {
	c.written.mu.Lock()
	defer c.written.mu.Unlock()
	for _, u := range uris {
		delete(c.written.m, normalizeActivationURI(u))
	}
}

// activateOwnPartsEnabled is on unless SAP_ACTIVATE_OWN_PARTS says otherwise.
func activateOwnPartsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SAP_ACTIVATE_OWN_PARTS"))) {
	case "false", "0", "no", "off":
		return false
	}
	return true
}

// ownedByThisProcess reports whether every inactive part is one this process
// wrote and nobody has touched since: it is in the registry, is held by the
// session user, and its inactive version still hashes to what was sent. Any
// doubt (no record, other user, unreadable, different content) answers false:
// the part is then treated as pending on purpose and is never activated.
func (c *Client) ownedByThisProcess(ctx context.Context, parts []InactiveObject) bool {
	if len(parts) == 0 || c.config == nil || c.config.Username == "" {
		return false
	}
	// The inactive version must come from SAP, not from the response cache. The
	// refused activation POST has already emptied it; this is belt and braces.
	c.InvalidateCache()
	for _, p := range parts {
		rec, ok := c.writtenPartFor(p.URI)
		if !ok || !strings.EqualFold(p.User, c.config.Username) {
			return false
		}
		inactive, err := c.readVersion(ctx, rec.sourceURL, "inactive")
		if err != nil || SourceHash(inactive) != rec.hash {
			return false
		}
	}
	return true
}

// readVersion reads one stored version ("inactive" or "active") of a source.
func (c *Client) readVersion(ctx context.Context, sourceURL, version string) (string, error) {
	q := url.Values{}
	q.Set("version", version)
	resp, err := c.transport.Request(ctx, sourceURL, &RequestOptions{
		Method: http.MethodGet,
		Query:  q,
		Accept: "text/plain",
	})
	if err != nil {
		return "", err
	}
	return string(resp.Body), nil
}

// parentIsOurs says whether the object being activated may go live along with
// its parts. SAP marks the parent (a function group, a class) inactive just
// because one of its parts changed, so being in the inactive list proves
// nothing. It is ours when this process wrote it and nobody changed it since,
// or when its own source has no pending change: the inactive and the active
// version are the same. If either cannot be read, it is not.
func (c *Client) parentIsOurs(ctx context.Context, objectURL string, parent InactiveObject) bool {
	if c.ownedByThisProcess(ctx, []InactiveObject{parent}) {
		return true
	}
	mainURL := strings.TrimSuffix(objectURL, "/") + "/source/main"
	inactive, err := c.readVersion(ctx, mainURL, "inactive")
	if err != nil {
		return false
	}
	active, err := c.readVersion(ctx, mainURL, "active")
	return err == nil && SourceHash(inactive) == SourceHash(active)
}

// activateOwnParts activates the object together with its inactive parts when
// all of them were written by this process. It returns nil when it does not
// apply (switch off, a foreign or unverifiable part), leaving the caller to
// name the parts as before.
func (c *Client) activateOwnParts(ctx context.Context, objectURL, objectName string, parent *InactiveObject, parts []InactiveObject) *ActivationResult {
	if !activateOwnPartsEnabled() || !c.ownedByThisProcess(ctx, parts) {
		return nil
	}
	// If the object itself is pending too, activating it would put its inactive
	// version live: that has to be ours as well.
	if parent != nil && !c.parentIsOurs(ctx, objectURL, *parent) {
		return nil
	}
	refs := []ObjectRef{{URI: objectURL, Name: objectName}}
	uris := make([]string, 0, len(parts))
	for _, p := range parts {
		refs = append(refs, ObjectRef{URI: p.URI, Name: p.Name})
		uris = append(uris, p.URI)
	}
	res, err := c.ActivateMultiple(ctx, refs)
	if err != nil {
		return &ActivationResult{
			Success:  false,
			Messages: []ActivationResultMessage{{Type: "E", ShortText: "Activating the object with the parts written in this session failed: " + err.Error()}},
			Inactive: parts,
		}
	}
	note := ActivationResultMessage{
		Type:      "I",
		ShortText: fmt.Sprintf("Activated together with the parts written by this session: %s", describeInactive(parts)),
	}
	res.Messages = append([]ActivationResultMessage{note}, res.Messages...)
	if res.Success {
		c.forgetWritten(append(uris, objectURL)...)
	}
	return res
}
