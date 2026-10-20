package adt

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// A dump carries the call stack at the moment of failure, and that is the
// difference between "something logged near this time" and "this frame was on
// the path". SAP serves it as part of the formatted dump: fixed-width text,
// two lines per frame, under a chapter called "Active Calls/Events".
//
// Parsing text is not the nicest thing in this file, and it is isolated here
// for that reason. Everything the listing and the grouping do stays on the
// structured feed; only this needs the text, and only callers that want the
// stack pay for it.

// DumpFrame is one entry of a dump's call stack, innermost first — SAP numbers
// them the other way round, highest at the top, and the order is preserved as
// SAP prints it.
type DumpFrame struct {
	Position int    `json:"position"`
	Type     string `json:"type"` // METHOD, FUNCTION, FORM, EVENT, MODULE (PBO)…
	Program  string `json:"program"`
	Include  string `json:"include,omitempty"`
	Line     int    `json:"line"`
	Name     string `json:"name,omitempty"` // CL_X=>METHOD, %_RFC_START, …
}

// ErrDumpDetailUnavailable says this release does not serve individual dumps.
//
// It is a different thing from a dump that could not be read, and worth
// separating: 7.50 has the feed but not the detail resource, exactly as it has
// the debugger but not /debugger/stack. Reporting that as a failure would put
// an alarming line under every dump on a system where nothing is wrong.
var ErrDumpDetailUnavailable = errors.New("this release does not serve individual dumps, so there is no call stack to read")

// formattedDump fetches the plain-text rendering of one dump.
//
// Everything brittle we read about a single dump — the stack, the header table,
// the termination point — comes out of this one document, so it is fetched
// once and parsed several times rather than fetched per field. The documents
// run from 45 KB to nearly a megabyte, and a similarity search reads one per
// candidate, so a second round trip per dump would be felt.
//
// The parsers match the English labels of the document, and the language of the
// text follows sap-language (checked live: with ES the labels come back
// translated, "Texto breve" for "Short Text", and every chapter would read as
// empty). So this always asks for English, whatever the session language is.
// The program source, variables and system fields are language neutral.
func (c *Client) formattedDump(ctx context.Context, dumpID string) (string, error) {
	if err := validDumpID(dumpID); err != nil {
		return "", err
	}
	path := dumpDetailPath(dumpID) + "/formatted"
	res, err := c.transport.Request(ctx, path, &RequestOptions{
		Method:           "GET",
		Accept:           "*/*",
		OverrideLanguage: "EN",
	})
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			return "", ErrDumpDetailUnavailable
		}
		return "", fmt.Errorf("reading the dump: %w", err)
	}
	return strings.ToValidUTF8(string(res.Body), "?"), nil
}

// dumpDetailPath turns a feed id into the detail resource. The feed points at
// the viewer resource under /vit/, which answers 404; the readable one drops
// that segment and is singular.
func dumpDetailPath(dumpID string) string {
	id := dumpID
	if i := strings.LastIndex(id, "/dumps/"); i >= 0 {
		id = id[i+len("/dumps/"):]
	}
	if i := strings.LastIndex(id, "/dump/"); i >= 0 {
		id = id[i+len("/dump/"):]
	}
	return "/sap/bc/adt/runtime/dump/" + id
}

// validDumpID keeps a caller-supplied id inside the dump collection: it is
// concatenated into a path, so path steps, query and fragment are refused.
func validDumpID(dumpID string) error {
	if strings.TrimSpace(dumpID) == "" || strings.Contains(dumpID, "..") || strings.ContainsAny(dumpID, "?#\\\r\n") {
		return fmt.Errorf("invalid dump id %q", dumpID)
	}
	return nil
}

const dumpStackChapter = "Active Calls/Events"

// parseDumpStack pulls the frames out of the formatted dump.
//
// Column positions shift between releases and even between rows — the line
// number is not always in the same place — so this reads from the ends inward
// rather than by offset: the last field is the line, before it the include,
// before that the program, and whatever remains between the frame number and
// the program is the type, which is sometimes two words ("MODULE (PBO)").
func parseDumpStack(formatted string) []DumpFrame {
	lines := strings.Split(formatted, "\n")

	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "|") && strings.Contains(line, dumpStackChapter) {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}

	var frames []DumpFrame
	for i := start + 1; i < len(lines); i++ {
		row := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(row, "|") {
			continue
		}
		// The chapter ends where the next one begins.
		if strings.Contains(row, "Selected Variables") {
			break
		}
		frame, ok := parseDumpFrameRow(row)
		if !ok {
			continue
		}
		// The name sits on the following row, alone.
		if i+1 < len(lines) {
			if name, ok := parseDumpFrameName(lines[i+1]); ok {
				frame.Name = name
				i++
			}
		}
		frames = append(frames, frame)
	}
	return frames
}

func parseDumpFrameRow(row string) (DumpFrame, bool) {
	fields := strings.Fields(strings.Trim(row, "|"))
	// number, type, program, include, line — five at the very least.
	if len(fields) < 5 {
		return DumpFrame{}, false
	}
	position, err := strconv.Atoi(fields[0])
	if err != nil {
		return DumpFrame{}, false
	}
	line, err := strconv.Atoi(fields[len(fields)-1])
	if err != nil {
		return DumpFrame{}, false
	}
	include := fields[len(fields)-2]
	program := fields[len(fields)-3]
	if include == "???" {
		include = ""
	}
	return DumpFrame{
		Position: position,
		Type:     strings.Join(fields[1:len(fields)-3], " "),
		Program:  program,
		Include:  include,
		Line:     line,
	}, true
}

// parseDumpFrameName reads the second row of a frame: one name, nothing else.
func parseDumpFrameName(row string) (string, bool) {
	trimmed := strings.TrimSpace(row)
	if !strings.HasPrefix(trimmed, "|") {
		return "", false
	}
	fields := strings.Fields(strings.Trim(trimmed, "|"))
	if len(fields) != 1 {
		return "", false
	}
	// A rule of dashes is not a name.
	if strings.Trim(fields[0], "-") == "" {
		return "", false
	}
	return fields[0], true
}
