package adt

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// --- i18n Types ---

// DataElementLabels holds the text labels of a data element in a specific
// language.
//
// The xml tags this carried — shortDescription, mediumDescription,
// longDescription and heading, all as attributes of the root element —
// described a response that does not exist. On a live 7.5x the labels are
// child elements of dtel:dataElement, named shortFieldLabel,
// mediumFieldLabel, longFieldLabel and headingFieldLabel. Nothing had ever
// matched, and nothing could: the request that would have carried the
// answer was refused with 406 before any parsing happened (Accept was the
// generic application/xml, not the versioned vocabulary type).
//
// The tags are gone rather than corrected, because this is the type a
// caller holds and the wire format is not its business. dataElementDoc
// below is the wire format.
type DataElementLabels struct {
	Short   string `json:"short"`
	Medium  string `json:"medium"`
	Long    string `json:"long"`
	Heading string `json:"heading"`
}

// DataElementLabelPatch is a partial update of a data element's labels: a
// nil pointer leaves that label unchanged, a non-nil one (including a
// pointer to "") sets it.
type DataElementLabelPatch struct {
	Short   *string `json:"short,omitempty"`
	Medium  *string `json:"medium,omitempty"`
	Long    *string `json:"long,omitempty"`
	Heading *string `json:"heading,omitempty"`
}

// checkLengths refuses a label past the fixed DDIC width for its kind
// (short 10, medium 20, long 40, heading 55) before any lock is taken —
// SAP would reject the PUT for the whole document anyway.
func (p DataElementLabelPatch) checkLengths() error {
	for _, f := range []struct {
		name string
		v    *string
		max  int
	}{
		{"short", p.Short, 10},
		{"medium", p.Medium, 20},
		{"long", p.Long, 40},
		{"heading", p.Heading, 55},
	} {
		if f.v != nil && len([]rune(*f.v)) > f.max {
			return fmt.Errorf("%s field label %q is %d characters; the DDIC limit is %d", f.name, *f.v, len([]rune(*f.v)), f.max)
		}
	}
	return nil
}

// dataElementDoc is the representation ADT actually serves for a data
// element: a blue:wbobj carrying the whole element. Only the four labels
// plus the read-only type facts the verify step compares are mapped —
// mapping the domain, the flags and the search help would be inventing a
// feature under cover of a bug fix, and the write path never round-trips
// through this struct (it substitutes the labels in the raw bytes) so the
// unmapped elements are not at risk.
type dataElementDoc struct {
	XMLName     xml.Name `xml:"wbobj"`
	DataElement struct {
		Short          string `xml:"shortFieldLabel"`
		Medium         string `xml:"mediumFieldLabel"`
		Long           string `xml:"longFieldLabel"`
		Heading        string `xml:"headingFieldLabel"`
		TypeKind       string `xml:"typeKind"`
		TypeName       string `xml:"typeName"`
		DataType       string `xml:"dataType"`
		DataTypeLength string `xml:"dataTypeLength"`
	} `xml:"dataElement"`
}

func (d dataElementDoc) labels() *DataElementLabels {
	return &DataElementLabels{
		Short:   d.DataElement.Short,
		Medium:  d.DataElement.Medium,
		Long:    d.DataElement.Long,
		Heading: d.DataElement.Heading,
	}
}

// TextPoolEntry represents a single text pool entry (text element/symbol) of a program.
type TextPoolEntry struct {
	ID   string `json:"id" xml:"id,attr"`
	Key  string `json:"key" xml:"key,attr"`
	Text string `json:"text" xml:"entry,attr"`
}

// LanguageComparison holds the result of comparing an object's texts in two languages.
type LanguageComparison struct {
	SourceLang string            `json:"sourceLang"`
	TargetLang string            `json:"targetLang"`
	Entries    []ComparisonEntry `json:"entries"`
}

// ComparisonEntry represents a single text key compared between two languages.
type ComparisonEntry struct {
	Key        string `json:"key"`
	SourceText string `json:"sourceText"`
	TargetText string `json:"targetText"`
	Missing    bool   `json:"missing"`
}

// --- i18n Methods ---

// GetObjectTextsInLanguage retrieves the source/content of an object in a specific language.
// objectSourceURL is the ADT source URL (e.g., /sap/bc/adt/programs/programs/ZTEST/source/main).
func (c *Client) GetObjectTextsInLanguage(ctx context.Context, objectSourceURL, lang string) (string, error) {
	if err := c.checkSafety(OpRead, "GetObjectTextsInLanguage"); err != nil {
		return "", err
	}

	lang = strings.ToUpper(lang)

	resp, err := c.transport.Request(ctx, objectSourceURL, &RequestOptions{
		Method:           http.MethodGet,
		OverrideLanguage: lang,
	})
	if err != nil {
		return "", fmt.Errorf("get object texts in language %s: %w", lang, err)
	}

	return string(resp.Body), nil
}

// GetDataElementLabels retrieves the text labels of a data element in a specific language.
func (c *Client) GetDataElementLabels(ctx context.Context, name, lang string) (*DataElementLabels, error) {
	if err := c.checkSafety(OpRead, "GetDataElementLabels"); err != nil {
		return nil, err
	}

	doc, _, err := c.getDataElementDoc(ctx, name, lang, false)
	if err != nil {
		return nil, err
	}
	// An element with no translation in the requested language answers in
	// its master language rather than empty, so a caller cannot read "these
	// are the English labels" out of a successful call. That is ADT's
	// behaviour and not something to paper over here.
	return doc.labels(), nil
}

// dataElementResourceURL is the ADT resource that serves and takes a data
// element's whole representation.
func dataElementResourceURL(name string) string {
	return fmt.Sprintf("/sap/bc/adt/ddic/dataelements/%s", url.PathEscape(strings.ToUpper(name)))
}

// getDataElementDoc GETs the blue:wbobj document of a data element and
// returns both the parsed view and the raw bytes. stateful must be true
// for the read that happens inside a lock window (issue #91).
//
// The versioned vocabulary type is required: the generic application/xml
// is refused with 406 "The message content is not acceptable" on every
// name, so this call had never returned a label to anybody.
func (c *Client) getDataElementDoc(ctx context.Context, name, lang string, stateful bool) (dataElementDoc, []byte, error) {
	resp, err := c.transport.Request(ctx, dataElementResourceURL(name), &RequestOptions{
		Method:           http.MethodGet,
		Accept:           "application/vnd.sap.adt.dataelements.v2+xml",
		OverrideLanguage: strings.ToUpper(lang),
		Stateful:         stateful,
	})
	if err != nil {
		return dataElementDoc{}, nil, fmt.Errorf("get data element %s: %w", strings.ToUpper(name), err)
	}
	var doc dataElementDoc
	if err := xml.Unmarshal(resp.Body, &doc); err != nil {
		return dataElementDoc{}, resp.Body, fmt.Errorf("parse data element %s: %w", strings.ToUpper(name), err)
	}
	return doc, resp.Body, nil
}

// GetMessageClassTexts retrieves all messages of a message class in a specific language.
func (c *Client) GetMessageClassTexts(ctx context.Context, name, lang string) ([]MessageClassMessage, error) {
	if err := c.checkSafety(OpRead, "GetMessageClassTexts"); err != nil {
		return nil, err
	}

	name = strings.ToUpper(name)
	lang = strings.ToUpper(lang)

	path := fmt.Sprintf("/sap/bc/adt/messageclass/%s", url.PathEscape(strings.ToLower(name)))
	resp, err := c.transport.Request(ctx, path, &RequestOptions{
		Method:           http.MethodGet,
		Accept:           "application/vnd.sap.adt.mc.messageclass+xml",
		OverrideLanguage: lang,
	})
	if err != nil {
		return nil, fmt.Errorf("get message class texts: %w", err)
	}

	var mc MessageClass
	if err := xml.Unmarshal(resp.Body, &mc); err != nil {
		return nil, fmt.Errorf("parse message class XML: %w", err)
	}

	return mc.Messages, nil
}

// WriteMessageClassTexts updates message class texts in a specific language.
// texts is an upsert by message number — a message omitted from both texts
// and deleteNumbers is left unchanged. deleteNumbers removes messages by
// number in the same PUT; pass nil if nothing is being deleted.
//
// STATUS as of this session's live testing against this project's own SAP
// system: this PUT does not currently persist a message, on either of the
// two independently-tried, namespace-correct body shapes (see
// messageClassWriteMessage's doc comment for the second attempt and its
// result). verifyMessageClassWrite below exists specifically because of
// this — it turns SAP's silent no-op into a returned error, so a caller
// never sees a false "success". Do not treat a nil error from this function
// as proof the write is fixed until that read-back verification is removed
// or this comment is.
//
// Requires a lock handle from LockObject and optionally a transport request
// number. Callers that don't want to manage a lock handle themselves should
// use WriteMessageClassTextsAutoLock instead.
//
// The Description SAP currently has is read first and echoed back on the PUT
// rather than left empty: an empty description attribute reportedly
// overwrites the message class's real short text (T100A/T100T) — see this
// function's package doc and the struct comment on MessageClass. UNVERIFIED
// against a live SAP system; confirm with a real PUT before relying on this
// in production.
//
// That read runs its own request rather than calling the public
// GetMessageClass, and deliberately after the lock, with Stateful: true: it
// happens inside the caller's lock window, and GetMessageClass's normal GET
// is stateless — a stateless hop between LOCK and this PUT would retire the
// very session the lock handle is bound to (issue #91).
func (c *Client) WriteMessageClassTexts(ctx context.Context, name, lang string, texts []MessageClassMessage, deleteNumbers []string, lockHandle, transport string) error {
	name = strings.ToUpper(name)
	lang = strings.ToUpper(lang)
	path := fmt.Sprintf("/sap/bc/adt/messageclass/%s", url.PathEscape(strings.ToLower(name)))

	// Unified mutation policy gate (op type + package + transport)
	if err := c.checkMutation(ctx, MutationContext{
		Op:        OpUpdate,
		OpName:    "WriteMessageClassTexts",
		ObjectURL: path,
		Transport: transport,
	}); err != nil {
		return err
	}

	// Echo the current description back rather than send an empty one — see
	// the doc comment above. A failure here is not fatal to the write: worst
	// case the description travels empty, same as before this fix.
	//
	// OverrideLanguage must match the PUT below: without it this GET reads
	// the session/logon language, not lang, so translating a class into a
	// language other than the session's would echo the description back in
	// the wrong language and overwrite the real one — the same class of
	// corruption this fix exists to prevent, just relocated from "empty" to
	// "wrong language".
	description := ""
	if resp, err := c.transport.Request(ctx, path, &RequestOptions{
		Method:           http.MethodGet,
		Accept:           "application/vnd.sap.adt.mc.messageclass+xml",
		OverrideLanguage: lang,
		Stateful:         true,
	}); err == nil {
		var current MessageClass
		if xml.Unmarshal(resp.Body, &current) == nil {
			description = current.Description
		}
	}

	// Build XML body. messageClassWriteBody, not MessageClass — see its doc
	// comment for why the write shape needs literal mc:/adtcore: prefixes
	// that the read type must not carry.
	mc := newMessageClassWriteBody(name, description)
	for _, m := range texts {
		mc.Messages = append(mc.Messages, messageClassWriteMessage{Number: m.Number, Text: m.Text})
	}
	for _, num := range deleteNumbers {
		mc.Deleted = append(mc.Deleted, messageClassDeletedMessage{Number: num})
	}
	body, err := xml.Marshal(mc)
	if err != nil {
		return fmt.Errorf("marshal message class XML: %w", err)
	}
	// An explicit XML prolog: upstream's own fix for this same defect sends
	// one, and this project's write attempt without it is exactly the one
	// that silently persisted nothing (see the doc comment on
	// messageClassWriteMessage) — matching or not, there's no reason not to
	// send what ADT's own real requests carry.
	body = append([]byte(xml.Header), body...)

	params := url.Values{}
	params.Set("lockHandle", lockHandle)
	if transport != "" {
		params.Set("corrNr", transport)
	}

	_, err = c.transport.Request(ctx, path, &RequestOptions{
		Method:           http.MethodPut,
		Query:            params,
		Body:             body,
		ContentType:      "application/vnd.sap.adt.mc.messageclass+xml",
		OverrideLanguage: lang,
		// The lockHandle in the query above came from a stateful LOCK and is
		// bound to that session. Without this the PUT that consumes it went
		// out explicitly stateless and could never match its own lock — the
		// same defect as CreateTable's source PUT (issue #91).
		Stateful: true,
	})
	if err != nil {
		return fmt.Errorf("write message class texts: %w", err)
	}

	// Read back and confirm the write actually took effect, rather than
	// trusting the PUT's 2xx. Confirmed live 2026-09-08 against this
	// project's own SAP system: a PUT shaped exactly like the one above
	// (root element/namespace correct, msgno/msgtext namespace-qualified per
	// the live-verified shape on messageClassWriteMessage) returned HTTP 200
	// with an empty body, and a subsequent GET showed no message had been
	// persisted at all — a silent no-op, not a reported failure. This
	// matches the mechanism the upstream issue this fix is based on
	// describes in CL_ADT_MC_RES_CONTROLLER=>DO_UPDATE: a body the content
	// handler cannot fully map is discarded after a `CHECK lr_data IS NOT
	// INITIAL`, with no error raised. The exact remaining gap in the shape
	// above is unconfirmed (see messageClassWriteMessage's doc comment) —
	// this check exists so that gap surfaces as an error instead of a false
	// "success" while it's being narrowed down, the same class of fix this
	// project has already applied to installers and other writes that used
	// to report success without verifying the result.
	if len(texts) > 0 || len(deleteNumbers) > 0 {
		if verifyErr := c.verifyMessageClassWrite(ctx, path, lang, texts, deleteNumbers); verifyErr != nil {
			return verifyErr
		}
	}

	return nil
}

// verifyMessageClassWrite reads a message class back, inside the same lock
// window (Stateful: true) as the PUT it is verifying, and confirms every
// expected text and deletion actually landed. See WriteMessageClassTexts's
// doc comment for why this exists: the PUT it follows has been observed to
// report success while silently writing nothing.
//
// A failure to even read back is not itself reported as a write failure —
// the PUT's own 2xx is the only signal available in that case, and this is a
// best-effort safety net, not the source of truth.
func (c *Client) verifyMessageClassWrite(ctx context.Context, path, lang string, texts []MessageClassMessage, deleteNumbers []string) error {
	resp, err := c.transport.Request(ctx, path, &RequestOptions{
		Method:           http.MethodGet,
		Accept:           "application/vnd.sap.adt.mc.messageclass+xml",
		OverrideLanguage: lang,
		Stateful:         true,
	})
	if err != nil {
		return nil
	}
	var after MessageClass
	if xml.Unmarshal(resp.Body, &after) != nil {
		return nil
	}

	byNumber := make(map[string]string, len(after.Messages))
	for _, m := range after.Messages {
		byNumber[m.Number] = m.Text
	}

	for _, want := range texts {
		if got, ok := byNumber[want.Number]; !ok || got != want.Text {
			return fmt.Errorf(
				"write message class texts: PUT returned success but message %s does not have the expected text after read-back "+
					"(got %q, want %q) — the write did not actually take effect", want.Number, got, want.Text)
		}
	}
	for _, num := range deleteNumbers {
		if _, stillThere := byNumber[num]; stillThere {
			return fmt.Errorf(
				"write message class texts: PUT returned success but message %s is still present after read-back — "+
					"the delete did not actually take effect", num)
		}
	}
	return nil
}

// WriteMessageClassTextsAutoLock updates message class texts, taking and
// releasing its own lock within the call. Intended for callers that don't
// manage lock handles themselves, such as the hyperfocused `edit MSAG` route
// — see WriteMessageClassTexts for the handle-supplied low-level form this
// wraps.
func (c *Client) WriteMessageClassTextsAutoLock(ctx context.Context, name, lang string, texts []MessageClassMessage, deleteNumbers []string, transport string) (err error) {
	name = strings.ToUpper(name)
	objectURL := fmt.Sprintf("/sap/bc/adt/messageclass/%s", url.PathEscape(strings.ToLower(name)))

	// Gate above the lock and mark the object so WriteMessageClassTexts's own
	// checkMutation below skips the redundant networked package lookup
	// inside the lock window — a stateless hop there would retire the
	// session the lock handle is bound to (issue #91).
	ctx, err = c.gateAndMark(ctx, MutationContext{
		Op:        OpUpdate,
		OpName:    "WriteMessageClassTexts",
		ObjectURL: objectURL,
		Transport: transport,
	})
	if err != nil {
		return err
	}

	var lockResult *LockResult
	lockResult, err = c.LockObject(ctx, objectURL, "MODIFY")
	if err != nil {
		return fmt.Errorf("failed to lock message class: %w", err)
	}

	// Ensure unlock. Detached from ctx's cancellation and given its own
	// deadline (issue #91) — a failure that cancelled ctx would otherwise
	// never send the compensating UNLOCK at all.
	unlocked := false
	defer func() {
		if !unlocked {
			if unlockErr := c.releaseLockAfterFailure(ctx, objectURL, lockResult.LockHandle); unlockErr != nil {
				err = fmt.Errorf("%w — %s", err, strandedLockAdvice(objectURL, unlockErr))
			}
		}
	}()

	// Adopt transport from lock result when caller did not supply one, same
	// as every other write path (issue #144/#91).
	var effectiveTransport string
	effectiveTransport, err = c.resolveWriteTransport(transport, lockResult.CorrNr, "WriteMessageClassTexts")
	if err != nil {
		return fmt.Errorf("transportable-edit check failed: %w", err)
	}

	if err = c.WriteMessageClassTexts(ctx, name, lang, texts, deleteNumbers, lockResult.LockHandle, effectiveTransport); err != nil {
		return err
	}

	err = c.UnlockObject(ctx, objectURL, lockResult.LockHandle)
	unlocked = true
	if err != nil {
		return fmt.Errorf("message class texts updated but unlock failed: %w", err)
	}

	return nil
}

// WriteDataElementLabels sets one or more of a data element's four field
// labels in a language, taking and releasing its own lock.
//
// It is read-modify-write, going beyond upstream (which neuters this to a
// "not implemented" error): the resource at /sap/bc/adt/ddic/dataelements/
// {name} serves and takes the element's *whole* blue:wbobj — domain, type,
// lengths, a dozen flags, search help, atom:links — and a PUT of a
// four-field document would either be rejected or, worse, accepted as a
// replacement for everything the element has. So the current document is
// GET first and only the four dtel:*FieldLabel values are substituted in
// the raw bytes; the unmodelled elements are carried through untouched
// because the bytes are never remarshalled from a struct.
//
// patch is partial: a nil field is left as it is, a non-nil one (including
// a pointer to "") is set. A label element the document does not contain is
// an error raised before the LOCK, so a doomed write never takes an
// ENQUEUE.
//
// Non-master-language behaviour: SAP serves untranslated labels in the
// master language, so patching e.g. only Short in German copies the
// master-language Medium/Long/Heading into the German row. That is
// documented, not guarded — a caller translating an element should pass all
// four labels.
func (c *Client) WriteDataElementLabels(ctx context.Context, name, lang string, patch DataElementLabelPatch, transport string) (err error) {
	name = strings.ToUpper(name)
	lang = strings.ToUpper(lang)
	objectURL := dataElementResourceURL(name)

	if lerr := patch.checkLengths(); lerr != nil {
		return lerr
	}

	// Gate above the lock and mark the object so the pre-write re-read
	// under the lock does not trigger a session-fatal package lookup
	// (issue #91).
	ctx, err = c.gateAndMark(ctx, MutationContext{
		Op:        OpUpdate,
		OpName:    "WriteDataElementLabels",
		ObjectURL: objectURL,
		Transport: transport,
	})
	if err != nil {
		return err
	}

	// Read the whole document before the lock, for the plan and to fail
	// early on a missing label element. This read is stateless on purpose —
	// it is before the lock window.
	_, preRaw, err := c.getDataElementDoc(ctx, name, lang, false)
	if err != nil {
		return err
	}
	if _, subErr := applyDataElementLabelPatch(preRaw, patch); subErr != nil {
		return subErr
	}

	trPlan := c.planTransport(ctx, transport, objectURL, "")

	var lockResult *LockResult
	lockResult, err = c.LockObject(ctx, objectURL, "MODIFY")
	if err != nil {
		return fmt.Errorf("failed to lock data element %s: %w", name, err)
	}
	unlocked := false
	defer func() {
		if !unlocked {
			if unlockErr := c.releaseLockAfterFailure(ctx, objectURL, lockResult.LockHandle); unlockErr != nil {
				err = joinLockReleaseErr(err, strandedLockAdvice(objectURL, unlockErr))
			}
		}
	}()

	var effectiveTransport string
	if effectiveTransport, _, err = c.resolveWriteTransportFor(trPlan, transport, lockResult.CorrNr, "WriteDataElementLabels"); err != nil {
		return err
	}

	// Re-read inside the lock window (Stateful) so the substitution runs on
	// exactly what SAP holds now, not the pre-lock snapshot.
	underDoc, underRaw, err := c.getDataElementDoc(ctx, name, lang, true)
	if err != nil {
		return err
	}
	newBody, err := applyDataElementLabelPatch(underRaw, patch)
	if err != nil {
		return err
	}

	params := url.Values{}
	params.Set("lockHandle", lockResult.LockHandle)
	if effectiveTransport != "" {
		params.Set("corrNr", effectiveTransport)
	}

	if _, err = c.transport.Request(ctx, objectURL, &RequestOptions{
		Method: http.MethodPut,
		Query:  params,
		Body:   newBody,
		// application/* is the ContentType CreateDataElement's whole-wbobj
		// PUT uses against this same resource and is proven to work; the
		// Accept matches the GET's versioned vocabulary type (the generic
		// one is 406'd here).
		ContentType:      "application/*",
		Accept:           "application/vnd.sap.adt.dataelements.v2+xml",
		OverrideLanguage: lang,
		// The lock handle came from a stateful LOCK a few lines up; without
		// this the PUT that consumes it goes out stateless and cannot match
		// its own lock (issue #91).
		Stateful: true,
	}); err != nil {
		return fmt.Errorf("write data element labels: %w", err)
	}

	err = c.UnlockObject(ctx, objectURL, lockResult.LockHandle)
	unlocked = true
	if err != nil {
		return fmt.Errorf("data element labels written but unlock failed: %w", err)
	}

	// Activate as its own object, after the unlock — a PUT lands as an
	// inactive version.
	activateCtx := context.WithoutCancel(ctx)
	if res, aerr := c.Activate(activateCtx, objectURL, name); aerr != nil {
		return fmt.Errorf("data element labels written but activation failed: %w", aerr)
	} else if res != nil && !res.Success {
		return fmt.Errorf("data element labels written but activation reported failure: %s", strings.Join(activationMessages(res), "; "))
	}

	// Read back and turn a silent no-op or a whole-object replacement into
	// an error. Best-effort: a read-back that itself fails is not a write
	// failure. underDoc is the pre-PUT state read under the lock.
	if verr := c.verifyDataElementLabelWrite(activateCtx, name, lang, patch, underDoc); verr != nil {
		return verr
	}
	return nil
}

// dtelFieldLabelElements maps a patch field to its dtel element name.
var dtelFieldLabelElements = []struct {
	name  string
	value func(DataElementLabelPatch) *string
}{
	{"shortFieldLabel", func(p DataElementLabelPatch) *string { return p.Short }},
	{"mediumFieldLabel", func(p DataElementLabelPatch) *string { return p.Medium }},
	{"longFieldLabel", func(p DataElementLabelPatch) *string { return p.Long }},
	{"headingFieldLabel", func(p DataElementLabelPatch) *string { return p.Heading }},
}

// applyDataElementLabelPatch substitutes the requested dtel:*FieldLabel
// values in the raw blue:wbobj bytes, leaving every other element — the
// domain, the flags, the search help, the atom:links — exactly as served.
// A requested label whose element is not in the document is an error
// (returned before any LOCK is taken).
func applyDataElementLabelPatch(raw []byte, patch DataElementLabelPatch) ([]byte, error) {
	out := raw
	for _, f := range dtelFieldLabelElements {
		v := f.value(patch)
		if v == nil {
			continue
		}
		next, err := replaceDataElementLabel(out, f.name, *v)
		if err != nil {
			return nil, err
		}
		out = next
	}
	return out, nil
}

// replaceDataElementLabel replaces the text content of one
// <dtel:LABEL>…</dtel:LABEL> element (or expands a self-closing
// <dtel:LABEL/>) with value, XML-escaped. The dtel: prefix is matched
// loosely (any or no prefix) so a differently-namespaced document still
// works. Not found → error.
func replaceDataElementLabel(xmlBytes []byte, elem, value string) ([]byte, error) {
	s := string(xmlBytes)
	esc := escapeXML(value)

	// <prefix:elem ...>old</prefix:elem>  or  <elem>old</elem>
	pair := regexp.MustCompile(`(?s)<((?:[A-Za-z_][\w.-]*:)?` + regexp.QuoteMeta(elem) + `)(\s[^>]*)?>.*?</((?:[A-Za-z_][\w.-]*:)?` + regexp.QuoteMeta(elem) + `>)`)
	if loc := pair.FindStringSubmatchIndex(s); loc != nil {
		return []byte(pair.ReplaceAllString(s, `<$1$2>`+regexpEscapeReplacement(esc)+`</$3`)), nil
	}

	// <prefix:elem ... />  self-closing
	selfClose := regexp.MustCompile(`<((?:[A-Za-z_][\w.-]*:)?` + regexp.QuoteMeta(elem) + `)(\s[^>]*?)?/>`)
	if selfClose.MatchString(s) {
		return []byte(selfClose.ReplaceAllString(s, `<$1$2>`+regexpEscapeReplacement(esc)+`</$1>`)), nil
	}

	return nil, fmt.Errorf("data element document has no <%s> element to set — the resource shape is not what this expects; use SE11", elem)
}

// regexpEscapeReplacement escapes the "$" that regexp.ReplaceAllString
// treats as a group reference, so an arbitrary label text is inserted
// literally.
func regexpEscapeReplacement(s string) string {
	return strings.ReplaceAll(s, "$", "$$")
}

// verifyDataElementLabelWrite reads the element back and asserts the
// patched labels landed and the type facts did not move (a
// whole-object-replacement detector). Best-effort: a read-back failure is
// not itself a write failure. before is the pre-PUT document read under
// the lock.
func (c *Client) verifyDataElementLabelWrite(ctx context.Context, name, lang string, patch DataElementLabelPatch, before dataElementDoc) error {
	after, _, err := c.getDataElementDoc(ctx, name, lang, false)
	if err != nil {
		return nil
	}
	got := map[string]string{
		"shortFieldLabel":   after.DataElement.Short,
		"mediumFieldLabel":  after.DataElement.Medium,
		"longFieldLabel":    after.DataElement.Long,
		"headingFieldLabel": after.DataElement.Heading,
	}
	for _, f := range dtelFieldLabelElements {
		v := f.value(patch)
		if v == nil {
			continue
		}
		// DDIC labels are fixed-width; SAP may serve one space-padded on the
		// right. Compare trailing-space-insensitive so a write that landed is
		// not reported as a failure over padding alone.
		if strings.TrimRight(got[f.name], " ") != strings.TrimRight(*v, " ") {
			return fmt.Errorf(
				"write data element labels: PUT returned success but %s reads %q after read-back, expected %q — the write did not take effect",
				f.name, got[f.name], *v)
		}
	}
	if before.DataElement.TypeName != "" && after.DataElement.TypeName != before.DataElement.TypeName {
		return fmt.Errorf(
			"write data element labels: the element's type changed from %q to %q after the write — the PUT was accepted as a whole-object replacement, not a label edit; check %s in SE11",
			before.DataElement.TypeName, after.DataElement.TypeName, name)
	}
	if before.DataElement.DataTypeLength != "" && after.DataElement.DataTypeLength != before.DataElement.DataTypeLength {
		return fmt.Errorf(
			"write data element labels: the element's length changed from %q to %q after the write — the PUT was accepted as a whole-object replacement; check %s in SE11",
			before.DataElement.DataTypeLength, after.DataElement.DataTypeLength, name)
	}
	return nil
}

// GetTextPoolInLanguage retrieves the text pool (text elements/symbols) of
// a program in a specific language.
//
// The address used to be /programs/programs/{name}/textelements, which
// answers 404 "No suitable resource found" — so this had never returned a
// text to anybody. The text pool is not a sub-resource of the program; it
// is its own resource, a container of three:
//
//	/sap/bc/adt/textelements/programs/{name}/source/symbols
//	                                        /source/selections
//	                                        /source/headings
//
// Each answers plain text — `key=value` per line, with an @MaxLength
// directive at the top of the symbols one — under its own vocabulary type.
// Asking with */* gets an HTML rendering that would parse to an empty pool.
func (c *Client) GetTextPoolInLanguage(ctx context.Context, programName, lang string) ([]TextPoolEntry, error) {
	if err := c.checkSafety(OpRead, "GetTextPoolInLanguage"); err != nil {
		return nil, err
	}

	programName = strings.ToUpper(programName)
	lang = strings.ToUpper(lang)

	var entries []TextPoolEntry
	for _, sub := range []struct{ name, id string }{
		{"symbols", "I"},
		{"selections", "S"},
		{"headings", "H"},
	} {
		path := fmt.Sprintf("/sap/bc/adt/textelements/programs/%s/source/%s",
			url.PathEscape(programName), sub.name)
		resp, err := c.transport.Request(ctx, path, &RequestOptions{
			Method:           http.MethodGet,
			Accept:           "application/vnd.sap.adt.textelements." + sub.name + ".v1",
			OverrideLanguage: lang,
		})
		if err != nil {
			// One missing kind is not a missing text pool: a report with
			// no selection screen has no selection texts, and that is an
			// answer. Treating the first 404 as fatal would lose the kinds
			// that did answer.
			if IsNotFoundError(err) {
				continue
			}
			return nil, fmt.Errorf("get text pool (%s): %w", sub.name, err)
		}
		entries = append(entries, parseTextPoolSource(sub.id, string(resp.Body))...)
	}

	return entries, nil
}

// parseTextPoolSource turns one text-element document into entries. Empty
// values are kept: "columnHeader_1=" means the heading exists and is
// untranslated, the single most useful thing a translation report can say.
func parseTextPoolSource(id, body string) []TextPoolEntry {
	var out []TextPoolEntry
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		// @MaxLength:8 and friends are directives about the document, not
		// entries in it.
		if strings.HasPrefix(strings.TrimSpace(line), "@") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" {
			continue
		}
		out = append(out, TextPoolEntry{ID: id, Key: key, Text: line[eq+1:]})
	}
	return out
}

// CompareObjectLanguages compares the text content of an object in two languages.
// Returns a comparison showing which texts differ or are missing in the target language.
func (c *Client) CompareObjectLanguages(ctx context.Context, objectSourceURL, sourceLang, targetLang string) (*LanguageComparison, error) {
	if err := c.checkSafety(OpRead, "CompareObjectLanguages"); err != nil {
		return nil, err
	}

	// Get source language content
	sourceContent, err := c.GetObjectTextsInLanguage(ctx, objectSourceURL, sourceLang)
	if err != nil {
		return nil, fmt.Errorf("get source language (%s): %w", sourceLang, err)
	}

	// Get target language content
	targetContent, err := c.GetObjectTextsInLanguage(ctx, objectSourceURL, targetLang)
	if err != nil {
		return nil, fmt.Errorf("get target language (%s): %w", targetLang, err)
	}

	// Build comparison by splitting into lines
	sourceLines := strings.Split(sourceContent, "\n")
	targetLines := strings.Split(targetContent, "\n")

	// Build target map for lookup
	targetMap := make(map[int]string)
	for i, line := range targetLines {
		targetMap[i] = line
	}

	comparison := &LanguageComparison{
		SourceLang: strings.ToUpper(sourceLang),
		TargetLang: strings.ToUpper(targetLang),
	}

	for i, sourceLine := range sourceLines {
		entry := ComparisonEntry{
			Key:        fmt.Sprintf("line-%d", i+1),
			SourceText: sourceLine,
		}
		if targetLine, ok := targetMap[i]; ok {
			entry.TargetText = targetLine
			entry.Missing = false
		} else {
			entry.Missing = true
		}
		if entry.SourceText != entry.TargetText || entry.Missing {
			comparison.Entries = append(comparison.Entries, entry)
		}
	}

	return comparison, nil
}
