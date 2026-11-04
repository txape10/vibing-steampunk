package adt

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// --- Enhancement Framework: reading ENHO implementations ---
//
// On S/4HANA 2023 an enhancement implementation (ENHO) is one of two kinds
// that matter here: ENHO/XHH, a source plug-in (ENHANCEMENT ... ENDENHANCEMENT
// blocks hooked into a program), and ENHO/XHB, a BAdI implementation, whose
// code lives in the implementing class. The type comes from the quick search,
// so no subtype is hard-coded.

// enhoNamePattern is what the name of an enhancement implementation or a BAdI
// may be (ENHNAME and BADI_NAME are CHAR 30, namespaces included). Names are
// put into free SQL, which has no bind parameters, so anything else is refused.
var enhoNamePattern = regexp.MustCompile(`^[A-Z0-9_/]{1,30}$`)

const (
	// enhoSiblingLimit caps the BAdI implementations listed for one BAdI.
	enhoSiblingLimit = 100
	// enhoActiveReadLimit caps the implementations whose active flag is read
	// one by one (each is a request).
	enhoActiveReadLimit = 25
)

// EnhancementHook is one hook point of a source plug-in (ENHINCINX row).
type EnhancementHook struct {
	ID       int    `json:"id"`
	Program  string `json:"program"`
	Position string `json:"position"` // FULL_NAME, e.g. \PR:SAPLV56K\FO:FORM\SE:END\EI
}

// BadiImplementation is one BAdI implementation. Active is "true", "false" or
// "" when it could not be read.
type BadiImplementation struct {
	Enhancement string `json:"enhancement"`
	Name        string `json:"name,omitempty"`
	Badi        string `json:"badi,omitempty"`
	Spot        string `json:"spot,omitempty"`
	Class       string `json:"class,omitempty"`
	Active      string `json:"active,omitempty"`
	Default     bool   `json:"default,omitempty"`
	ShortText   string `json:"shortText,omitempty"`
	Position    int    `json:"position,omitempty"`
}

// Enhancement is what is read of one ENHO.
type Enhancement struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"` // XHH, XHB, ...
	Description string `json:"description,omitempty"`
	Package     string `json:"package,omitempty"`
	Responsible string `json:"responsible,omitempty"`
	// Source is the plug-in code of an XHH.
	Source string            `json:"source,omitempty"`
	Hooks  []EnhancementHook `json:"hooks,omitempty"`
	// Implementations are the BAdI implementations of an XHB. For each BAdI it
	// implements, Others lists every implementation of that BAdI on the system.
	Implementations []BadiImplementation `json:"implementations,omitempty"`
	Others          []BadiImplementation `json:"otherImplementationsOfTheSameBadi,omitempty"`
	Note            string               `json:"note,omitempty"`
}

// enhoXHB is the ENHO/XHB resource.
type enhoXHB struct {
	Description string `xml:"description,attr"`
	Responsible string `xml:"responsible,attr"`
	PackageRef  struct {
		Name string `xml:"name,attr"`
	} `xml:"packageRef"`
	Impls []struct {
		Name      string `xml:"name,attr"`
		ShortText string `xml:"shortText,attr"`
		Active    string `xml:"active,attr"`
		Default   string `xml:"default,attr"`
		Spot      struct {
			Name string `xml:"name,attr"`
		} `xml:"enhancementSpot"`
		Badi struct {
			Name string `xml:"name,attr"`
		} `xml:"badiDefinition"`
		Class struct {
			Name string `xml:"name,attr"`
		} `xml:"implementingClass"`
	} `xml:"contentSpecific>badiTechnology>badiImplementations>badiImplementation"`
}

// getEnhancementText is GetSource for ENHO: the plug-in code of an XHH
// (hook points as leading comments), the JSON of an XHB.
func (c *Client) getEnhancementText(ctx context.Context, name string) (string, error) {
	enh, err := c.GetEnhancement(ctx, name)
	if err != nil {
		return "", err
	}
	if enh.Kind != "XHH" {
		data, err := json.MarshalIndent(enh, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to serialize enhancement: %w", err)
		}
		return string(data), nil
	}
	var b strings.Builder
	if enh.Note != "" {
		fmt.Fprintf(&b, "* NOTE: %s\n", enh.Note)
	}
	for _, h := range enh.Hooks {
		fmt.Fprintf(&b, "* ENHANCEMENT %d hooked at %s in %s\n", h.ID, h.Position, h.Program)
	}
	b.WriteString(enh.Source)
	return b.String(), nil
}

// GetEnhancement reads an enhancement implementation by name.
func (c *Client) GetEnhancement(ctx context.Context, name string) (*Enhancement, error) {
	if err := c.checkSafety(OpRead, "GetEnhancement"); err != nil {
		return nil, err
	}
	name = strings.ToUpper(strings.TrimSpace(name))
	if !enhoNamePattern.MatchString(name) {
		return nil, fmt.Errorf("invalid enhancement name %q: expected up to 30 letters, digits, or _ /", name)
	}
	hits, incomplete, err := c.SearchObjectExact(ctx, name, "ENHO", 5)
	if err != nil {
		return nil, fmt.Errorf("searching enhancement %s: %w", name, err)
	}
	var enhos []SearchResult
	for _, h := range hits {
		if strings.HasPrefix(strings.ToUpper(h.Type), "ENHO") {
			enhos = append(enhos, h)
		}
	}
	switch len(enhos) {
	case 0:
		return nil, fmt.Errorf("enhancement implementation %s not found", name)
	case 1:
	default:
		return nil, fmt.Errorf("enhancement name %s is ambiguous (%d matches)", name, len(enhos))
	}
	hit := enhos[0]
	enh := &Enhancement{
		Name:        name,
		Kind:        strings.TrimPrefix(strings.ToUpper(hit.Type), "ENHO/"),
		Description: hit.Description,
		Package:     hit.PackageName,
		Responsible: hit.ResponsiblePro,
		Note:        incomplete,
	}
	base := strings.TrimRight(hit.URI, "/")
	if base == "" {
		return nil, fmt.Errorf("enhancement %s: the search returned no URI", name)
	}

	switch enh.Kind {
	case "XHH":
		err = c.readEnhancementSource(ctx, enh, base)
	case "XHB":
		err = c.readBadiImplementation(ctx, enh, base)
	default:
		return nil, fmt.Errorf("enhancement %s is of type ENHO/%s, which is not read yet (supported: XHH source plug-ins, XHB BAdI implementations)", name, enh.Kind)
	}
	if err != nil {
		return nil, err
	}
	return enh, nil
}

// readEnhancementSource reads the code of a source plug-in and, from
// ENHINCINX, where each ENHANCEMENT block is hooked.
func (c *Client) readEnhancementSource(ctx context.Context, enh *Enhancement, base string) error {
	resp, err := c.transport.Request(ctx, base+"/source/main", &RequestOptions{Method: http.MethodGet, Accept: "text/plain"})
	if err != nil {
		return fmt.Errorf("reading source of enhancement %s: %w", enh.Name, err)
	}
	enh.Source = string(resp.Body)

	res, err := c.GetTableContents(ctx, "ENHINCINX", 200,
		"SELECT enhname, programname, id, full_name FROM enhincinx WHERE enhname = '"+enh.Name+"' AND version = 'A'")
	if err != nil {
		enh.Note = joinNotes(enh.Note, fmt.Sprintf("hook points not read, ENHINCINX: %v", err))
		return nil
	}
	for _, row := range res.Rows {
		id, _ := strconv.Atoi(cellString(row, "ID"))
		enh.Hooks = append(enh.Hooks, EnhancementHook{ID: id, Program: cellString(row, "PROGRAMNAME"), Position: cellString(row, "FULL_NAME")})
	}
	sort.Slice(enh.Hooks, func(i, j int) bool { return enh.Hooks[i].ID < enh.Hooks[j].ID })
	return nil
}

// readBadiImplementation reads an XHB: the class, BAdI, spot and active flag
// of its implementations, and every other implementation of the same BAdIs.
func (c *Client) readBadiImplementation(ctx context.Context, enh *Enhancement, base string) error {
	doc, err := c.fetchXHB(ctx, base)
	if err != nil {
		return fmt.Errorf("reading BAdI implementation %s: %w", enh.Name, err)
	}
	if doc.Description != "" {
		enh.Description = doc.Description
	}
	if doc.PackageRef.Name != "" {
		enh.Package = doc.PackageRef.Name
	}
	if doc.Responsible != "" {
		enh.Responsible = doc.Responsible
	}
	seen := map[string]bool{}
	var badis []string
	for _, i := range doc.Impls {
		enh.Implementations = append(enh.Implementations, BadiImplementation{
			Enhancement: enh.Name, Name: i.Name, Badi: i.Badi.Name, Spot: i.Spot.Name, Class: i.Class.Name,
			Active: i.Active, Default: i.Default == "true", ShortText: i.ShortText,
		})
		if i.Badi.Name != "" && !seen[i.Badi.Name] {
			seen[i.Badi.Name] = true
			badis = append(badis, i.Badi.Name)
		}
	}
	for _, b := range badis {
		others, note := c.badiImplementations(ctx, b)
		enh.Note = joinNotes(enh.Note, note)
		for _, o := range others {
			if o.Enhancement != enh.Name {
				enh.Others = append(enh.Others, o)
			}
		}
	}
	return nil
}

func (c *Client) fetchXHB(ctx context.Context, base string) (*enhoXHB, error) {
	resp, err := c.transport.Request(ctx, base, &RequestOptions{Method: http.MethodGet, Accept: "*/*"})
	if err != nil {
		return nil, err
	}
	var doc enhoXHB
	if err := xml.Unmarshal(resp.Body, &doc); err != nil {
		return nil, fmt.Errorf("parsing the BAdI implementation: %w", err)
	}
	return &doc, nil
}

// BadiImplementations lists every implementation of a BAdI (BADI_IMPL) with
// its class and, where it can be read, whether it is active.
func (c *Client) BadiImplementations(ctx context.Context, badi string) ([]BadiImplementation, string, error) {
	if err := c.checkSafety(OpRead, "BadiImplementations"); err != nil {
		return nil, "", err
	}
	badi = strings.ToUpper(strings.TrimSpace(badi))
	if !enhoNamePattern.MatchString(badi) {
		return nil, "", fmt.Errorf("invalid BAdI name %q: expected up to 30 letters, digits, or _ /", badi)
	}
	out, note := c.badiImplementations(ctx, badi)
	if note != "" && len(out) == 0 {
		return nil, "", errors.New(note)
	}
	return out, note, nil
}

// badiImplementations is the unchecked core; badi must already be validated
// or come from the whitelist below.
func (c *Client) badiImplementations(ctx context.Context, badi string) ([]BadiImplementation, string) {
	if !enhoNamePattern.MatchString(badi) {
		return nil, fmt.Sprintf("BAdI %q skipped: its name is not a plain identifier", badi)
	}
	res, err := c.GetTableContents(ctx, "BADI_IMPL", enhoSiblingLimit+1,
		"SELECT badi_name, pos, enhname, badi_impl, class_name, is_default FROM badi_impl WHERE badi_name = '"+badi+"' ORDER BY pos")
	if err != nil {
		return nil, fmt.Sprintf("implementations of %s not read, BADI_IMPL: %v", badi, err)
	}
	var notes []string
	var out []BadiImplementation
	for _, row := range res.Rows {
		pos, _ := strconv.Atoi(cellString(row, "POS"))
		out = append(out, BadiImplementation{
			Enhancement: cellString(row, "ENHNAME"), Name: cellString(row, "BADI_IMPL"), Badi: badi,
			Class: cellString(row, "CLASS_NAME"), Default: cellString(row, "IS_DEFAULT") == "X", Position: pos,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	if len(out) > enhoSiblingLimit {
		out = out[:enhoSiblingLimit]
		notes = append(notes, fmt.Sprintf("%s: only the first %d implementations are listed", badi, enhoSiblingLimit))
	}
	// The active flag is only in the ENHO itself: one read per implementation.
	read, failed := 0, 0
	var firstErr error
	for i := range out {
		if !enhoNamePattern.MatchString(out[i].Enhancement) {
			continue
		}
		if read >= enhoActiveReadLimit {
			notes = append(notes, fmt.Sprintf("%s: active state read for the first %d implementations only", badi, enhoActiveReadLimit))
			break
		}
		read++
		doc, err := c.fetchXHB(ctx, "/sap/bc/adt/enhancements/enhoxhb/"+url.PathEscape(strings.ToLower(out[i].Enhancement)))
		if err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
			// Without access every further read would fail the same way.
			if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "403") {
				break
			}
			continue
		}
		for _, im := range doc.Impls {
			if strings.EqualFold(im.Name, out[i].Name) && strings.EqualFold(im.Badi.Name, badi) {
				out[i].Active, out[i].Spot, out[i].ShortText = im.Active, im.Spot.Name, im.ShortText
			}
		}
	}
	if failed > 0 {
		notes = append(notes, fmt.Sprintf("%s: active state not read for %d implementation(s): %v", badi, failed, firstErr))
	}
	return out, strings.Join(notes, "; ")
}

func joinNotes(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "; " + b
}
