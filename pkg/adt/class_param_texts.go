package adt

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Short texts of method parameters (SE24) live in SEOSUBCOTX. The ABAP Doc
// "shorttext synchronized" tag that covers methods and attributes does not
// reach them, and ADT has no resource for them, so they go through the
// report domain of the ZADT_VSP WebSocket service (SEO_PARAMETER_MODIFY on
// the SAP side).

// maxParamTextLength is the width of SEODESCR (CHAR 60).
const maxParamTextLength = 60

// transportNumberPattern is a workbench request number: <SID>K<6 digits>.
var transportNumberPattern = regexp.MustCompile(`^[A-Z0-9]{3}K[0-9]{6}$`)

// ClassParamText is one parameter of a method and its short text.
type ClassParamText struct {
	Name        string `json:"name"`
	DeclType    string `json:"decl_type"` // 0 importing, 1 exporting, 2 changing, 3 returning
	Version     string `json:"version"`   // active | inactive | both
	Description string `json:"description"`
	HasText     bool   `json:"has_text"`
}

// ClassMethodParamTexts groups the parameters of one method.
type ClassMethodParamTexts struct {
	MethodName string           `json:"method_name"`
	CmpType    string           `json:"cmp_type"` // 0 attribute, 1 method, 2 event
	Params     []ClassParamText `json:"params"`
}

// ClassParamTexts is the answer of getClassParamTexts.
type ClassParamTexts struct {
	ClassName      string                  `json:"class_name"`
	PackageName    string                  `json:"package_name"`
	MasterLanguage string                  `json:"master_language"`
	Language       string                  `json:"language"`
	Methods        []ClassMethodParamTexts `json:"method_list"`
}

// SetClassParamTextParams are the inputs of SetClassParamText. Clear must be
// set to empty a text; an empty Description without it is refused. Transport
// is the workbench request a change in a transportable package is recorded in;
// it is never chosen or created for the caller.
type SetClassParamTextParams struct {
	Class       string
	Method      string
	Parameter   string
	Description string
	Language    string // "" = master language; 1 char = SAP key; 2 chars = ISO
	Clear       bool
	Transport   string
}

// SetClassParamTextResult is the answer of setClassParamText.
type SetClassParamTextResult struct {
	Status         string `json:"status"` // changed | unchanged
	ClassName      string `json:"class_name"`
	MethodName     string `json:"method_name"`
	ParameterName  string `json:"parameter_name"`
	Language       string `json:"language"`
	MasterLanguage string `json:"master_language"`
	OldDescription string `json:"old_description"`
	NewDescription string `json:"new_description"`
	RowState       string `json:"row_state"` // present | blank | absent
	Verified       bool   `json:"verified"`
	Transport      string `json:"transport,omitempty"`
	Recording      string `json:"recording,omitempty"` // recorded | not_needed
}

// NormalizeTransport trims and upper-cases a request number.
func NormalizeTransport(transport string) string {
	return strings.ToUpper(strings.TrimSpace(transport))
}

// ValidateClassParamTextParams checks everything that can be decided without
// talking to SAP, so a bad request never opens the WebSocket.
func ValidateClassParamTextParams(p SetClassParamTextParams) error {
	if strings.TrimSpace(p.Class) == "" || strings.TrimSpace(p.Method) == "" || strings.TrimSpace(p.Parameter) == "" {
		return fmt.Errorf("class, method and parameter are required")
	}
	// Blanks around the text do not survive SAP's CHAR60, so they do not count.
	desc := strings.TrimSpace(p.Description)
	if p.Clear && desc != "" {
		return fmt.Errorf("clear cannot be combined with a description")
	}
	if !p.Clear && desc == "" {
		return fmt.Errorf("an empty description needs clear=true")
	}
	if n := utf8.RuneCountInString(desc); n > maxParamTextLength {
		return fmt.Errorf("description has %d characters, the maximum is %d", n, maxParamTextLength)
	}
	for _, r := range desc {
		if unicode.IsControl(r) {
			return fmt.Errorf("description must not contain control characters (line breaks, tabs)")
		}
		// SAP stores UCS-2: anything beyond the BMP (emoji) would not round-trip.
		if r > 0xFFFF {
			return fmt.Errorf("description contains a character outside the Basic Multilingual Plane")
		}
	}
	if tr := NormalizeTransport(p.Transport); tr != "" && !transportNumberPattern.MatchString(tr) {
		return fmt.Errorf("transport %q is not a request number (expected <SID>K<6 digits>)", p.Transport)
	}
	return validateParamTextLanguage(p.Language)
}

// validateParamTextLanguage accepts "", a one-character SAP key, or a
// two-letter ISO code known to isoToSAPLang. spras() is deliberately not
// used: for an unmapped ISO code it guesses the first letter.
func validateParamTextLanguage(lang string) error {
	l := strings.ToUpper(strings.TrimSpace(lang))
	switch utf8.RuneCountInString(l) {
	case 0, 1:
		return nil
	case 2:
		if _, ok := isoToSAPLang[l]; ok {
			return nil
		}
	}
	return fmt.Errorf("unknown language %q: use a one-character SAP key or a known two-letter ISO code", lang)
}

// GetClassParamTexts reads the parameter texts of a class, optionally for
// one method and one language (default: the class master language).
func (c *AMDPWebSocketClient) GetClassParamTexts(ctx context.Context, class, method, language string) (*ClassParamTexts, error) {
	if strings.TrimSpace(class) == "" {
		return nil, fmt.Errorf("class is required")
	}
	if err := validateParamTextLanguage(language); err != nil {
		return nil, err
	}
	params := map[string]interface{}{"class_name": class}
	if method != "" {
		params["method_name"] = method
	}
	if language != "" {
		params["language"] = language
	}

	resp, err := c.sendReportRequest(ctx, "getClassParamTexts", params)
	if err != nil {
		return nil, wrapParamTextsError(err)
	}
	var result ClassParamTexts
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &result); err != nil {
			return nil, fmt.Errorf("failed to parse result: %w", err)
		}
	}
	return &result, nil
}

// SetClassParamText writes (or clears) one parameter text. A write SAP
// accepted but whose read-back differs is returned as an error.
func (c *AMDPWebSocketClient) SetClassParamText(ctx context.Context, p SetClassParamTextParams) (*SetClassParamTextResult, error) {
	if err := ValidateClassParamTextParams(p); err != nil {
		return nil, err
	}
	params := map[string]interface{}{
		"class_name":     p.Class,
		"method_name":    p.Method,
		"parameter_name": p.Parameter,
		"description":    strings.TrimSpace(p.Description),
		"clear_text":     p.Clear,
	}
	if p.Language != "" {
		params["language"] = p.Language
	}
	if tr := NormalizeTransport(p.Transport); tr != "" {
		params["transport"] = tr
	}

	resp, err := c.sendReportRequest(ctx, "setClassParamText", params)
	if err != nil {
		return nil, wrapParamTextsError(err)
	}
	var result SetClassParamTextResult
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &result); err != nil {
			return nil, fmt.Errorf("failed to parse result: %w", err)
		}
	}
	if !result.Verified {
		return &result, fmt.Errorf("SAP accepted the write but the text could not be verified; check SEOSUBCOTX before retrying")
	}
	return &result, nil
}

// wrapParamTextsError explains the one failure that is not about the request:
// the ABAP backend predates the two actions.
func wrapParamTextsError(err error) error {
	if strings.HasPrefix(err.Error(), "UNKNOWN_ACTION") {
		return fmt.Errorf("%w — the ZADT_VSP backend (ZCL_VSP_REPORT_SERVICE) does not have this action yet, or this vsp process still holds an old WebSocket session: deploy the class and restart Claude Desktop", err)
	}
	return err
}
