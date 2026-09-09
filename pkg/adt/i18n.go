package adt

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// --- i18n Types ---

// DataElementLabels holds the text labels of a data element in a specific language.
type DataElementLabels struct {
	Short   string `json:"short" xml:"shortDescription,attr"`
	Medium  string `json:"medium" xml:"mediumDescription,attr"`
	Long    string `json:"long" xml:"longDescription,attr"`
	Heading string `json:"heading" xml:"heading,attr"`
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

	name = strings.ToUpper(name)
	lang = strings.ToUpper(lang)

	path := fmt.Sprintf("/sap/bc/adt/ddic/dataelements/%s", url.PathEscape(name))
	resp, err := c.transport.Request(ctx, path, &RequestOptions{
		Method:           http.MethodGet,
		Accept:           "application/xml",
		OverrideLanguage: lang,
	})
	if err != nil {
		return nil, fmt.Errorf("get data element labels: %w", err)
	}

	// Parse the XML - data element labels are attributes on the root element
	var labels DataElementLabels
	if err := xml.Unmarshal(resp.Body, &labels); err != nil {
		return nil, fmt.Errorf("parse data element labels: %w", err)
	}

	return &labels, nil
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

// WriteDataElementLabels updates data element labels in a specific language.
// Requires a lock handle from LockObject and optionally a transport request number.
func (c *Client) WriteDataElementLabels(ctx context.Context, name, lang string, labels *DataElementLabels, lockHandle, transport string) error {
	name = strings.ToUpper(name)
	lang = strings.ToUpper(lang)

	// Unified mutation policy gate (op type + package + transport)
	if err := c.checkMutation(ctx, MutationContext{
		Op:        OpUpdate,
		OpName:    "WriteDataElementLabels",
		ObjectURL: fmt.Sprintf("/sap/bc/adt/ddic/dataelements/%s", url.PathEscape(name)),
		Transport: transport,
	}); err != nil {
		return err
	}

	body, err := xml.Marshal(labels)
	if err != nil {
		return fmt.Errorf("marshal data element labels: %w", err)
	}

	path := fmt.Sprintf("/sap/bc/adt/ddic/dataelements/%s", url.PathEscape(name))

	params := url.Values{}
	params.Set("lockHandle", lockHandle)
	if transport != "" {
		params.Set("corrNr", transport)
	}

	_, err = c.transport.Request(ctx, path, &RequestOptions{
		Method:           http.MethodPut,
		Query:            params,
		Body:             body,
		ContentType:      "application/xml",
		OverrideLanguage: lang,
	})
	if err != nil {
		return fmt.Errorf("write data element labels: %w", err)
	}

	return nil
}

// GetTextPoolInLanguage retrieves the text pool (text elements/symbols) of a program in a specific language.
func (c *Client) GetTextPoolInLanguage(ctx context.Context, programName, lang string) ([]TextPoolEntry, error) {
	if err := c.checkSafety(OpRead, "GetTextPoolInLanguage"); err != nil {
		return nil, err
	}

	programName = strings.ToUpper(programName)
	lang = strings.ToUpper(lang)

	path := fmt.Sprintf("/sap/bc/adt/programs/programs/%s/textelements", url.PathEscape(programName))
	resp, err := c.transport.Request(ctx, path, &RequestOptions{
		Method:           http.MethodGet,
		Accept:           "application/xml",
		OverrideLanguage: lang,
	})
	if err != nil {
		return nil, fmt.Errorf("get text pool: %w", err)
	}

	type textPool struct {
		Entries []TextPoolEntry `xml:"entry"`
	}
	var tp textPool
	if err := xml.Unmarshal(resp.Body, &tp); err != nil {
		return nil, fmt.Errorf("parse text pool XML: %w", err)
	}

	return tp.Entries, nil
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
