package adt

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Object description management: read and change the short text SE80/SE11
// shows next to an object's name (adtcore:description), without touching the
// source.
//
// Ported from upstream oisee/vibing-steampunk PR #201 (the set_description
// half; the vsp-update self-updater from the same PR is deliberately not
// ported — it would overwrite this fork's binary with upstream's).
//
// Both forks already set adtcore:description in the shell POST when an object
// is *created*. Neither could change it afterwards: WriteSource only reads
// opts.Description on its create branch. This closes that gap.
//
// The description lives as an attribute on the object's metadata document,
// which is the object's whole representation (type, package, flags,
// atom:links). A struct remarshal would drop everything not modelled, so the
// current document is GET first and only the adtcore:description attribute is
// substituted in the raw bytes — the same read-modify-write discipline
// WriteDataElementLabels uses for a data element's labels.
//
// Session-affinity (issue #91): the pre-write re-read runs inside the lock
// window with Stateful:true, the PUT is Stateful:true, and gateAndMark runs
// the package gate above the lock so nothing networked hops inside it.
//
// Live-verification status: the write is verified end to end for PROG (and,
// by shape, INCL). CLAS/INTF/FUGR/FUNC/TABL/DDLS use the same metadata
// resource and the same adtcore:description attribute, but whether every one
// of them accepts a whole-document PUT the same way is unconfirmed on a real
// system. verifyDescriptionWrite guards the two ways that could go wrong —
// a silent no-op and a whole-object replacement — so a bad outcome surfaces
// as an error rather than a false success.

// DescriptionResult is the outcome of a read or a write.
type DescriptionResult struct {
	ObjectURL     string   `json:"objectUrl"`
	Old           string   `json:"old"`
	New           string   `json:"new"`
	Changed       bool     `json:"changed"`
	Transport     string   `json:"transport,omitempty"`
	TransportNote string   `json:"transportNote,omitempty"`
	Limit         int      `json:"limit,omitempty"`
	Notes         []string `json:"notes,omitempty"`
}

// descriptionAttrRE matches the adtcore:description attribute (any or no
// namespace prefix) and captures its value. The value group is [^"]* so an
// empty description ("") matches too. descriptionTextLimit is not matched:
// it ends in "TextLimit=", not "description=". All matching is confined to
// the root element's opening tag (see rootOpenTag) so a child element that
// happens to carry its own description is never touched.
var descriptionAttrRE = regexp.MustCompile(`(\s(?:[A-Za-z_][\w.-]*:)?description=")([^"]*)(")`)

// descriptionLimitRE reads the DDIC width limit ADT advertises for the
// description, when it does (adtcore:descriptionTextLimit="70").
var descriptionLimitRE = regexp.MustCompile(`(?:[A-Za-z_][\w.-]*:)?descriptionTextLimit="([0-9]+)"`)

// rootNameRE finds the name="..." attribute (any or no prefix) — the
// insertion point, within the root tag, when a document carries no
// description attribute at all.
var rootNameRE = regexp.MustCompile(`\s(?:[A-Za-z_][\w.-]*:)?name="[^"]*"`)

// rootElementRE captures the root element's local name (prefix stripped),
// used to detect a PUT that replaced the object with one of another kind.
var rootElementRE = regexp.MustCompile(`<(?:[A-Za-z_][\w.-]*:)?([A-Za-z_][\w.-]*)[\s>]`)

// rootOpenTag returns the byte range of the document's root element opening
// tag — from "<localname" to the ">" that closes it — skipping any <?xml?>
// prolog and <!-- --> / <!DOCTYPE> nodes before it. The closing ">" is found
// respecting quoted attribute values, so a literal ">" inside an attribute
// does not cut the tag short. (-1, -1) when there is no element at all.
func rootOpenTag(s string) (start, end int) {
	i := 0
	for {
		lt := strings.IndexByte(s[i:], '<')
		if lt < 0 {
			return -1, -1
		}
		lt += i
		if strings.HasPrefix(s[lt:], "<?") || strings.HasPrefix(s[lt:], "<!") {
			gt := strings.IndexByte(s[lt:], '>')
			if gt < 0 {
				return -1, -1
			}
			i = lt + gt + 1
			continue
		}
		gt := tagCloseIndex(s[lt:])
		if gt < 0 {
			return -1, -1
		}
		return lt, lt + gt + 1
	}
}

// tagCloseIndex returns the index of the ">" that closes the element tag
// starting at s[0], skipping any ">" inside a single- or double-quoted
// attribute value. -1 if the tag is unterminated.
func tagCloseIndex(s string) int {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return i
		}
	}
	return -1
}

// DescriptionObjectURL maps an object type + name (+ parent for FUNC) to the
// ADT resource whose metadata document carries the description.
//
// The eight types are upstream #201's set: PROG, INCL, CLAS, INTF, FUGR,
// FUNC, TABL, DDLS. The fork's own DDIC creators (DOMA/DTEL/TTYP/ENQU) are
// not here on purpose — their "description" is the ddtext inside a per-type
// blue:wbobj, a different shape that needs its own investigation, and DTEL's
// is easily confused with WriteDataElementLabels' field labels.
func DescriptionObjectURL(objectType, name, parent string) (string, error) {
	objectType = strings.ToUpper(strings.TrimSpace(objectType))
	name = strings.ToUpper(strings.TrimSpace(name))
	parent = strings.ToUpper(strings.TrimSpace(parent))
	if name == "" {
		return "", fmt.Errorf("object name is required")
	}
	switch objectType {
	case "", "PROG":
		return "/sap/bc/adt/programs/programs/" + url.PathEscape(name), nil
	case "INCL":
		return "/sap/bc/adt/programs/includes/" + url.PathEscape(name), nil
	case "CLAS":
		return "/sap/bc/adt/oo/classes/" + url.PathEscape(name), nil
	case "INTF":
		return "/sap/bc/adt/oo/interfaces/" + url.PathEscape(name), nil
	case "FUGR":
		return "/sap/bc/adt/functions/groups/" + url.PathEscape(name), nil
	case "FUNC":
		if parent == "" {
			return "", fmt.Errorf("FUNC needs its function group as parent")
		}
		return "/sap/bc/adt/functions/groups/" + url.PathEscape(parent) + "/fmodules/" + url.PathEscape(name), nil
	case "TABL":
		return "/sap/bc/adt/ddic/tables/" + url.PathEscape(strings.ToLower(name)), nil
	case "DDLS":
		return "/sap/bc/adt/ddic/ddl/sources/" + url.PathEscape(strings.ToLower(name)), nil
	default:
		return "", fmt.Errorf("set_description does not handle object type %q — it is one of PROG, INCL, CLAS, INTF, FUGR, FUNC, TABL, DDLS", objectType)
	}
}

// getObjectMetadataRaw GETs an object's metadata document as raw bytes.
// stateful must be true for the read inside a lock window (issue #91).
//
// application/* is the Accept upstream #201 settled on (its literal is */*);
// there is no single versioned vocabulary type across the eight object
// types, and this one is what CreateDataElement's whole-document PUT already
// uses against a DDIC resource.
func (c *Client) getObjectMetadataRaw(ctx context.Context, objectURL string, stateful bool) ([]byte, error) {
	resp, err := c.transport.Request(ctx, objectURL, &RequestOptions{
		Method:   http.MethodGet,
		Accept:   "application/*",
		Stateful: stateful,
	})
	if err != nil {
		return nil, fmt.Errorf("get metadata for %s: %w", strings.TrimPrefix(objectURL, "/sap/bc/adt/"), err)
	}
	return resp.Body, nil
}

// parseDescriptionDoc pulls the current description (XML-unescaped) and the
// advertised character limit (0 when the document does not state one) out of
// a metadata document's root element.
func parseDescriptionDoc(raw []byte) (description string, limit int) {
	s := string(raw)
	rs, re := rootOpenTag(s)
	if rs < 0 {
		return "", 0
	}
	root := s[rs:re]
	if m := descriptionAttrRE.FindStringSubmatch(root); m != nil {
		description = html.UnescapeString(m[2])
	}
	if m := descriptionLimitRE.FindStringSubmatch(root); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			limit = n
		}
	}
	return description, limit
}

// rootLocalName returns the document root element's local name (namespace
// prefix stripped), or "".
func rootLocalName(raw []byte) string {
	if m := rootElementRE.FindSubmatch(raw); m != nil {
		return string(m[1])
	}
	return ""
}

// replaceDescriptionAttr returns raw with the root element's
// adtcore:description value set to newDesc (XML-escaped). When the root has
// no description attribute the attribute is inserted after its name
// attribute; when there is no name either, an error is returned before any
// PUT. Only the root opening tag is edited — a child element's own
// description, if any, is left alone.
func replaceDescriptionAttr(raw []byte, newDesc string) ([]byte, error) {
	s := string(raw)
	esc := escapeXML(newDesc)

	rs, re := rootOpenTag(s)
	if rs < 0 {
		return nil, fmt.Errorf(
			"the object's metadata document has no root element to attach a description to — the resource shape is not what this expects; set the description in SE80/SE11")
	}
	root := s[rs:re]

	if loc := descriptionAttrRE.FindStringSubmatchIndex(root); loc != nil {
		// loc[4]:loc[5] is the value capture group, relative to root.
		newRoot := root[:loc[4]] + esc + root[loc[5]:]
		return []byte(s[:rs] + newRoot + s[re:]), nil
	}

	if loc := rootNameRE.FindStringIndex(root); loc != nil {
		at := loc[1] // just past name="..."
		newRoot := root[:at] + ` adtcore:description="` + esc + `"` + root[at:]
		return []byte(s[:rs] + newRoot + s[re:]), nil
	}

	return nil, fmt.Errorf(
		"the object's metadata document root has neither a description nor a name attribute to attach one to — the resource shape is not what this expects; set the description in SE80/SE11")
}

// GetDescription reads an object's current short description and, when ADT
// states it, the character limit.
func (c *Client) GetDescription(ctx context.Context, objectType, name, parent string) (*DescriptionResult, error) {
	if err := c.checkSafety(OpRead, "GetDescription"); err != nil {
		return nil, err
	}
	objectURL, err := DescriptionObjectURL(objectType, name, parent)
	if err != nil {
		return nil, err
	}
	raw, err := c.getObjectMetadataRaw(ctx, objectURL, false)
	if err != nil {
		return nil, err
	}
	desc, limit := parseDescriptionDoc(raw)
	return &DescriptionResult{ObjectURL: objectURL, Old: desc, New: desc, Changed: false, Limit: limit}, nil
}

// SetDescription changes an object's short description in the session
// language, taking and releasing its own lock. It is a no-op — and takes no
// lock — when the description is already what is asked for.
func (c *Client) SetDescription(ctx context.Context, objectType, name, parent, description, transport string) (result *DescriptionResult, err error) {
	objectURL, err := DescriptionObjectURL(objectType, name, parent)
	if err != nil {
		return nil, err
	}
	name = strings.ToUpper(strings.TrimSpace(name))
	result = &DescriptionResult{ObjectURL: objectURL, New: description}

	// Gate above the lock and mark the object so the re-read under the lock
	// does not trigger a session-fatal package lookup (issue #91).
	ctx, err = c.gateAndMark(ctx, MutationContext{
		Op:        OpUpdate,
		OpName:    "SetDescription",
		ObjectURL: objectURL,
		Transport: transport,
	})
	if err != nil {
		return result, err
	}

	// Read before the lock: current text, the limit, and to fail early on a
	// document with nowhere to put a description. Stateless on purpose — it
	// is outside the lock window.
	preRaw, err := c.getObjectMetadataRaw(ctx, objectURL, false)
	if err != nil {
		return result, err
	}
	current, limit := parseDescriptionDoc(preRaw)
	result.Old, result.Limit = current, limit

	if limit > 0 && len([]rune(description)) > limit {
		return result, fmt.Errorf("description is %d characters; the limit for %s is %d", len([]rune(description)), name, limit)
	}
	if current == description {
		result.Changed = false
		return result, nil
	}
	if _, subErr := replaceDescriptionAttr(preRaw, description); subErr != nil {
		return result, subErr
	}

	trPlan := c.planTransport(ctx, transport, objectURL, "")

	var lock *LockResult
	lock, err = c.LockObject(ctx, objectURL, "MODIFY", trPlan.lockCorrNr(transport))
	if err != nil {
		return result, fmt.Errorf("failed to lock %s: %w", name, err)
	}
	unlocked := false
	defer func() {
		if !unlocked {
			if unlockErr := c.releaseLockAfterFailure(ctx, objectURL, lock.LockHandle); unlockErr != nil {
				err = joinLockReleaseErr(err, strandedLockAdvice(objectURL, unlockErr))
			}
		}
	}()

	var effectiveTransport, note string
	if effectiveTransport, note, err = c.resolveWriteTransportFor(trPlan, transport, lock.CorrNr, "SetDescription"); err != nil {
		return result, err
	}
	result.Transport, result.TransportNote = effectiveTransport, note

	// Re-read inside the lock window (Stateful) so the substitution runs on
	// exactly what SAP holds now, not the pre-lock snapshot.
	underRaw, err := c.getObjectMetadataRaw(ctx, objectURL, true)
	if err != nil {
		return result, err
	}
	newBody, err := replaceDescriptionAttr(underRaw, description)
	if err != nil {
		return result, err
	}
	rootBefore := rootLocalName(underRaw)

	params := url.Values{}
	params.Set("lockHandle", lock.LockHandle)
	if effectiveTransport != "" {
		params.Set("corrNr", effectiveTransport)
	}
	if _, err = c.transport.Request(ctx, objectURL, &RequestOptions{
		Method:      http.MethodPut,
		Query:       params,
		Body:        newBody,
		ContentType: "application/*",
		Accept:      "application/*",
		// The lock handle came from a stateful LOCK; without this the PUT
		// that consumes it goes out stateless and cannot match its own lock
		// (issue #91).
		Stateful: true,
	}); err != nil {
		return result, fmt.Errorf("write description: %w", err)
	}

	err = c.UnlockObject(ctx, objectURL, lock.LockHandle)
	unlocked = true
	if err != nil {
		return result, fmt.Errorf("description written but unlock failed: %w", err)
	}

	// A metadata PUT may land as an inactive version — activate as its own
	// object after the unlock. Best-effort: whether a description-only change
	// even needs this is unverified, so an activation problem is a note, not
	// a failed write.
	activateCtx := context.WithoutCancel(ctx)
	if res, aerr := c.Activate(activateCtx, objectURL, name); aerr != nil {
		result.Notes = append(result.Notes, fmt.Sprintf("activation after the write failed: %v", aerr))
	} else if res != nil && !res.Success {
		result.Notes = append(result.Notes, "activation after the write reported failure: "+strings.Join(activationMessages(res), "; "))
	}

	// Read back and turn a silent no-op — or a PUT that replaced the object
	// with a different kind — into an error. Best-effort: a read-back that
	// itself fails is not a write failure.
	if verr := c.verifyDescriptionWrite(activateCtx, objectURL, description, rootBefore); verr != nil {
		return result, verr
	}

	result.Changed = true
	return result, nil
}

// verifyDescriptionWrite reads the object back and confirms the new
// description is what SAP now serves and that the PUT did not swap the
// object for one of another kind (rootBefore is the root element's local
// name as read under the lock). A read-back failure is not itself reported
// as a write failure.
func (c *Client) verifyDescriptionWrite(ctx context.Context, objectURL, want, rootBefore string) error {
	raw, err := c.getObjectMetadataRaw(ctx, objectURL, false)
	if err != nil {
		return nil
	}
	if rootBefore != "" {
		if after := rootLocalName(raw); after != "" && after != rootBefore {
			return fmt.Errorf(
				"write description: %s came back as <%s> after the write, was <%s> — the PUT was accepted as a whole-object replacement, not a description edit; check the object in SE80/SE11",
				strings.TrimPrefix(objectURL, "/sap/bc/adt/"), after, rootBefore)
		}
	}
	got, _ := parseDescriptionDoc(raw)
	if strings.TrimRight(got, " ") != strings.TrimRight(want, " ") {
		return fmt.Errorf(
			"write description: PUT returned success but %s reads %q after read-back, expected %q — the change did not take effect (it may have landed as an inactive version; check the object in SE80/SE11)",
			strings.TrimPrefix(objectURL, "/sap/bc/adt/"), got, want)
	}
	return nil
}
