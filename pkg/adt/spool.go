package adt

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/oisee/vibing-steampunk/pkg/temse"
)

// The spool is three ordinary tables. TSP01 is the request: number, owner,
// title, when, what kind of document. TST01 is the TemSe object the request
// points at, with its code page and where it is stored. TST03 is that object's
// content, in blocks, when it is stored in the database (the default); a system
// that keeps spool in files has the TST01 row and nothing in TST03, and then
// only XBP over RFC (pkg/saprfc) can return the content. TBTCP ties a spool to
// the background job step that wrote it.
//
// TSP01 is restricted to the logon client by an explicit RQCLIENT term.
// Created times are as SAP stored them (server time), shown in the local zone.
//
// Reading a spool this way goes by table, not through the spool authorization
// objects (S_SPO_ACT, owner checks): whoever may run free SQL on these tables
// may read any spool of the logon client.

const (
	spoolDefaultLimit  = 50
	spoolMaxLimit      = 200
	spoolDefaultWindow = 7 * 24 * time.Hour
	// temseMaxBytes caps what one TemSe object may return, so a huge list
	// cannot fill the memory of the server or the context of the model.
	temseMaxBytes = 20 << 20
	// temseBlockRows is how many TST03 rows one query asks for.
	temseBlockRows = 200
	// spoolRQ2Len is the width of RQ2NAME, where the writer puts the program
	// followed by the first letters of the user.
	spoolRQ2Len = 12
)

var (
	spoolTitleRe = regexp.MustCompile(`^[A-Za-z0-9 _\-./*+:,()#]{1,68}$`)
	temseNameRe  = regexp.MustCompile(`^[A-Za-z0-9_]{1,20}$`)
)

// SpoolRequest is one TSP01 row with what TST01 and TBTCP add to it.
type SpoolRequest struct {
	Number  int       `json:"number"`
	Client  string    `json:"client,omitempty"`
	Owner   string    `json:"owner"`
	Created time.Time `json:"created"`
	Title   string    `json:"title,omitempty"`
	// Suffixes are TSP01's three name parts (RQ0NAME, RQ1NAME, RQ2NAME):
	// usually the list kind, the device and the program that wrote it.
	Suffixes []string `json:"suffixes,omitempty"`
	// DocType is LIST for an ABAP list, OTF for SAPscript, RAW, or a PDF kind.
	DocType string `json:"docType,omitempty"`
	Device  string `json:"device,omitempty"`
	Pages   int    `json:"pages,omitempty"`
	Copies  int    `json:"copies,omitempty"`
	// TemSe is the object holding the content, and Storage where it is:
	// D in the database, F in a file.
	TemSe   string `json:"temse,omitempty"`
	Storage string `json:"storage,omitempty"`
	Lines   int    `json:"lines,omitempty"`
	Bytes   int    `json:"bytes,omitempty"`
	// Codepage is the content's SAP code page (4103 on Unicode systems).
	Codepage string `json:"codepage,omitempty"`
	// Job is the background job step that produced the request, when one did.
	Job *SpoolJobRef `json:"job,omitempty"`
}

// SpoolJobRef is the TBTCP row pointing at a spool.
type SpoolJobRef struct {
	Name    string `json:"name"`
	Count   string `json:"count"`
	Step    int    `json:"step"`
	Program string `json:"program,omitempty"`
	Variant string `json:"variant,omitempty"`
	User    string `json:"user,omitempty"`
}

// SpoolFilter narrows a listing. Every text value is checked against a
// whitelist before it reaches SQL.
type SpoolFilter struct {
	Owner   string
	Title   string // a LIKE pattern with * wildcards
	Program string // the program that wrote the list (first 12 characters)
	Job     string // a job name; only spools written by that job's steps
	From    time.Time
	To      time.Time
	Limit   int // default 50, at most 200
}

// SpoolRequests lists spool requests of the logon client, newest first.
// Without any filter it covers the last seven days.
func (c *Client) SpoolRequests(ctx context.Context, filter SpoolFilter) ([]SpoolRequest, error) {
	if err := c.checkSafety(OpRead, "SpoolRequests"); err != nil {
		return nil, err
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = spoolDefaultLimit
	}
	if limit > spoolMaxLimit {
		limit = spoolMaxLimit
	}

	client, err := c.spoolClient()
	if err != nil {
		return nil, err
	}
	terms := []string{"rqclient = '" + client + "'"}
	unfiltered := true
	if v := strings.ToUpper(strings.TrimSpace(filter.Owner)); v != "" {
		unfiltered = false
		if !jobUserRe.MatchString(v) {
			return nil, fmt.Errorf("invalid owner %q: expected up to 12 letters, digits or _", filter.Owner)
		}
		terms = append(terms, "rqowner = '"+v+"'")
	}
	if v := strings.TrimSpace(filter.Title); v != "" {
		unfiltered = false
		if !spoolTitleRe.MatchString(v) {
			return nil, fmt.Errorf("invalid title pattern %q: expected up to 68 letters, digits, blanks or _ - . / * + : , ( ) #", filter.Title)
		}
		terms = append(terms, "rqtitle LIKE '"+strings.ReplaceAll(v, "*", "%")+"'")
	}
	var program string
	if v := strings.ToUpper(strings.TrimSpace(filter.Program)); v != "" {
		unfiltered = false
		if !jobProgramRe.MatchString(v) {
			return nil, fmt.Errorf("invalid program %q: expected up to 40 letters, digits, _ or /", filter.Program)
		}
		if len(v) > spoolRQ2Len {
			v = v[:spoolRQ2Len]
		}
		program = v
		// RQ2NAME is the program followed by the user's first letters, so it is
		// a prefix match; the underscore of a program name is exact in Go below.
		terms = append(terms, "rq2name LIKE '"+program+"%'")
	}
	from, to := filter.From, filter.To
	if !from.IsZero() || !to.IsZero() {
		unfiltered = false
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		return nil, fmt.Errorf("the start of the period (%s) is after its end (%s)", from.Format("2006-01-02"), to.Format("2006-01-02"))
	}
	var byJob map[int]SpoolJobRef
	if job := strings.ToUpper(strings.TrimSpace(filter.Job)); job != "" {
		unfiltered = false
		if !ValidJobName(job) {
			return nil, fmt.Errorf("invalid job name %q: expected up to 32 letters, digits, or _ - /", filter.Job)
		}
		refs, err := c.spoolJobRefs(ctx, "jobname = '"+job+"' AND listident <> '0000000000'", jobsMaxLimit*5)
		if err != nil {
			return nil, err
		}
		if len(refs) == 0 {
			return nil, nil
		}
		byJob = refs
		ids := make([]string, 0, len(refs))
		for n := range refs {
			ids = append(ids, strconv.Itoa(n))
		}
		sort.Strings(ids)
		terms = append(terms, "rqident IN ( "+strings.Join(ids, ", ")+" )")
	}
	if unfiltered {
		from = time.Now().Add(-spoolDefaultWindow)
	}
	if !from.IsZero() {
		terms = append(terms, "rqcretime >= '"+from.Format("20060102150405")+"'")
	}
	if !to.IsZero() {
		// RQCRETIME is YYYYMMDDHHMMSS plus two digits; a date as the end of the
		// period covers that whole day.
		end := to
		if end.Hour() == 0 && end.Minute() == 0 && end.Second() == 0 {
			end = end.Add(24*time.Hour - time.Second)
		}
		terms = append(terms, "rqcretime <= '"+end.Format("20060102150405")+"99'")
	}

	query := spoolSelect + " FROM tsp01"
	if len(terms) > 0 {
		query += " WHERE " + strings.Join(terms, " AND ")
	}
	query += " ORDER BY rqcretime DESCENDING"
	// A program filter is finished in Go (the underscore is a LIKE wildcard), so
	// ask for more rows than the limit to still fill it.
	rows := limit
	if program != "" {
		rows = limit * 4
	}
	res, err := c.RunQuery(ctx, query, rows)
	if err != nil {
		return nil, fmt.Errorf("reading spool requests: %w", err)
	}
	if res == nil {
		return nil, nil
	}
	var out []SpoolRequest
	for _, row := range res.Rows {
		r := spoolRequestFromRow(row)
		if program != "" && !strings.HasPrefix(r.Suffixes[2], program) {
			continue
		}
		out = append(out, r)
		if len(out) == limit {
			break
		}
	}
	if len(out) == 0 {
		return out, nil
	}

	names := make([]string, 0, len(out))
	ids := make([]string, 0, len(out))
	for _, r := range out {
		if temseNameRe.MatchString(r.TemSe) {
			names = append(names, r.TemSe)
		}
		ids = append(ids, strconv.Itoa(r.Number))
	}
	headers, err := c.temseHeaders(ctx, names)
	if err != nil {
		return nil, err
	}
	if byJob == nil {
		if byJob, err = c.spoolJobRefs(ctx, "listident IN ( "+strings.Join(padSpoolIDs(ids), ", ")+" )", len(ids)*2); err != nil {
			return nil, err
		}
	}
	for i := range out {
		if h, ok := headers[out[i].TemSe]; ok {
			out[i].Storage, out[i].Codepage, out[i].Lines, out[i].Bytes = h.Storage, h.Codepage, h.Rows, h.Size
		}
		if ref, ok := byJob[out[i].Number]; ok {
			ref := ref
			out[i].Job = &ref
		}
	}
	return out, nil
}

// spoolClient is the logon client, checked as 3 digits; TSP01 keeps it in a
// plain field, so the data preview does not restrict by it on its own.
func (c *Client) spoolClient() (string, error) {
	client := strings.TrimSpace(c.SAPClient())
	if !jobClientRe.MatchString(client) {
		return "", fmt.Errorf("spool: the logon client %q is not a 3-digit client", client)
	}
	return client, nil
}

const spoolSelect = "SELECT rqident, rqclient, rqowner, rqcretime, rqtitle, rq0name, rq1name, rq2name, rqdoctype, rqdest, rqcopies, rqpjreq, rqo1name"

func spoolRequestFromRow(row map[string]interface{}) SpoolRequest {
	r := SpoolRequest{
		Client: jobCell(row, "RQCLIENT"), Owner: jobCell(row, "RQOWNER"), Title: jobCell(row, "RQTITLE"),
		DocType: jobCell(row, "RQDOCTYPE"), Device: jobCell(row, "RQDEST"), TemSe: jobCell(row, "RQO1NAME"),
		Created: parseSpoolTime(jobCell(row, "RQCRETIME")),
	}
	r.Number, _ = strconv.Atoi(jobCell(row, "RQIDENT"))
	r.Copies, _ = strconv.Atoi(jobCell(row, "RQCOPIES"))
	r.Pages, _ = strconv.Atoi(jobCell(row, "RQPJREQ"))
	for _, k := range []string{"RQ0NAME", "RQ1NAME", "RQ2NAME"} {
		r.Suffixes = append(r.Suffixes, jobCell(row, k))
	}
	return r
}

// padSpoolIDs writes spool numbers the way TBTCP-LISTIDENT stores them: ten
// digits, quoted.
func padSpoolIDs(ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = "'" + strings.Repeat("0", max(10-len(id), 0)) + id + "'"
	}
	return out
}

// spoolJobRefs reads TBTCP rows and keys them by spool number. The where clause
// is built by the callers from validated values only.
func (c *Client) spoolJobRefs(ctx context.Context, where string, limit int) (map[int]SpoolJobRef, error) {
	res, err := c.RunQuery(ctx, "SELECT jobname, jobcount, stepcount, progname, variant, authcknam, listident FROM tbtcp WHERE "+where, limit)
	if err != nil {
		return nil, fmt.Errorf("reading job steps: %w", err)
	}
	out := map[int]SpoolJobRef{}
	if res == nil {
		return out, nil
	}
	for _, row := range res.Rows {
		n, _ := strconv.Atoi(jobCell(row, "LISTIDENT"))
		if n == 0 {
			continue
		}
		step, _ := strconv.Atoi(jobCell(row, "STEPCOUNT"))
		out[n] = SpoolJobRef{Name: jobCell(row, "JOBNAME"), Count: jobCell(row, "JOBCOUNT"), Step: step,
			Program: jobCell(row, "PROGNAME"), Variant: jobCell(row, "VARIANT"), User: jobCell(row, "AUTHCKNAM")}
	}
	return out, nil
}

// TemSeHeader is what TST01 says about an object.
type TemSeHeader struct {
	Name       string `json:"name"`
	Type       string `json:"type,omitempty"`
	RecordType string `json:"recordType,omitempty"`
	Codepage   string `json:"codepage,omitempty"`
	Storage    string `json:"storage,omitempty"`
	Rows       int    `json:"rows,omitempty"`
	Size       int    `json:"size,omitempty"`
	LineLength int    `json:"lineLength,omitempty"`
	Parts      int    `json:"parts,omitempty"`
	Creator    string `json:"creator,omitempty"`
}

// temseHeaders reads TST01 for the given (already validated) object names.
func (c *Client) temseHeaders(ctx context.Context, names []string) (map[string]TemSeHeader, error) {
	out := map[string]TemSeHeader{}
	if len(names) == 0 {
		return out, nil
	}
	quoted := make([]string, len(names))
	for i, n := range names {
		if !temseNameRe.MatchString(n) {
			return nil, fmt.Errorf("invalid TemSe object name %q", n)
		}
		quoted[i] = "'" + n + "'"
	}
	res, err := c.RunQuery(ctx, "SELECT dname, dtype, drectyp, dcharcod, dstotyp, drows, dsize, dlinelen, dnoparts, dcreater FROM tst01 WHERE dname IN ( "+strings.Join(quoted, ", ")+" )", len(quoted)*2)
	if err != nil {
		return nil, fmt.Errorf("reading TemSe headers: %w", err)
	}
	if res == nil {
		return out, nil
	}
	for _, row := range res.Rows {
		h := TemSeHeader{Name: jobCell(row, "DNAME"), Type: jobCell(row, "DTYPE"), RecordType: jobCell(row, "DRECTYP"),
			Codepage: jobCell(row, "DCHARCOD"), Storage: jobCell(row, "DSTOTYP"), Creator: jobCell(row, "DCREATER")}
		h.Rows, _ = strconv.Atoi(jobCell(row, "DROWS"))
		h.Size, _ = strconv.Atoi(jobCell(row, "DSIZE"))
		h.LineLength, _ = strconv.Atoi(jobCell(row, "DLINELEN"))
		h.Parts, _ = strconv.Atoi(jobCell(row, "DNOPARTS"))
		out[h.Name] = h
	}
	return out, nil
}

// TemSeObject reads one TemSe object's header and content from TST01/TST03.
// The content is read in blocks of temseBlockRows rows and refused above
// temseMaxBytes.
func (c *Client) TemSeObject(ctx context.Context, name string) (*TemSeHeader, []byte, error) {
	if err := c.checkSafety(OpRead, "TemSeObject"); err != nil {
		return nil, nil, err
	}
	name = strings.ToUpper(strings.TrimSpace(name))
	if !temseNameRe.MatchString(name) {
		return nil, nil, fmt.Errorf("invalid TemSe object name %q: expected up to 20 letters, digits or _", name)
	}
	headers, err := c.temseHeaders(ctx, []string{name})
	if err != nil {
		return nil, nil, err
	}
	h, ok := headers[name]
	if !ok {
		return nil, nil, fmt.Errorf("TemSe object %s is not in TST01", name)
	}
	if h.Storage != "" && h.Storage != "D" {
		return &h, nil, fmt.Errorf("TemSe object %s is stored in %s, not the database; only XBP over RFC can read it", name, storageName(h.Storage))
	}
	if h.Size > temseMaxBytes {
		return &h, nil, fmt.Errorf("TemSe object %s is %d bytes, over the %d byte limit", name, h.Size, temseMaxBytes)
	}
	var data []byte
	parts := max(h.Parts, 1)
	for part := 1; part <= parts; part++ {
		// Page by key, not by counting from 1, so a gap in DROWNO cannot end the
		// read early.
		last := 0
		for {
			res, err := c.RunQuery(ctx, fmt.Sprintf("SELECT drowno, ddatalen, dcontent FROM tst03 WHERE dname = '%s' AND dpart = %d AND drowno BETWEEN %d AND %d ORDER BY drowno", name, part, last+1, math.MaxInt32), temseBlockRows)
			if err != nil {
				return &h, nil, fmt.Errorf("reading TemSe content of %s: %w", name, err)
			}
			if res == nil || len(res.Rows) == 0 {
				break
			}
			for _, row := range res.Rows {
				block, err := decodeHexCell(jobCell(row, "DCONTENT"))
				if err != nil {
					return &h, nil, fmt.Errorf("TemSe object %s: %w", name, err)
				}
				if n, _ := strconv.Atoi(jobCell(row, "DDATALEN")); n > 0 && n < len(block) {
					block = block[:n]
				}
				if len(data)+len(block) > temseMaxBytes {
					return &h, nil, fmt.Errorf("TemSe object %s is over the %d byte limit", name, temseMaxBytes)
				}
				data = append(data, block...)
				if no, _ := strconv.Atoi(jobCell(row, "DROWNO")); no > last {
					last = no
				}
			}
			if len(res.Rows) < temseBlockRows {
				break
			}
		}
	}
	if len(data) == 0 {
		return &h, nil, fmt.Errorf("TemSe object %s has no content rows in TST03", name)
	}
	return &h, data, nil
}

func storageName(code string) string {
	switch code {
	case "F":
		return "a file on the application server"
	case "D":
		return "the database"
	}
	return "storage type " + code
}

// SpoolContent is one spool request with its content read and, for a list,
// decoded.
type SpoolContent struct {
	Request SpoolRequest `json:"request"`
	Header  *TemSeHeader `json:"temse,omitempty"`
	// List is the decoded ABAP list for DocType LIST.
	List *temse.List `json:"list,omitempty"`
	// Raw is the content of any other document type, as stored.
	Raw []byte `json:"raw,omitempty"`
}

// Spool reads one spool request of the logon client and its content over ADT.
func (c *Client) Spool(ctx context.Context, number int) (*SpoolContent, error) {
	if err := c.checkSafety(OpRead, "Spool"); err != nil {
		return nil, err
	}
	if number <= 0 {
		return nil, fmt.Errorf("invalid spool request number %d", number)
	}
	client, err := c.spoolClient()
	if err != nil {
		return nil, err
	}
	res, err := c.RunQuery(ctx, spoolSelect+" FROM tsp01 WHERE rqclient = '"+client+"' AND rqident = "+strconv.Itoa(number), 1)
	if err != nil {
		return nil, fmt.Errorf("reading spool request %d: %w", number, err)
	}
	if res == nil || len(res.Rows) == 0 {
		return nil, fmt.Errorf("spool request %d does not exist in this client", number)
	}
	req := spoolRequestFromRow(res.Rows[0])
	req.Number = number
	if refs, err := c.spoolJobRefs(ctx, "listident = '"+fmt.Sprintf("%010d", number)+"'", 2); err == nil {
		if ref, ok := refs[number]; ok {
			req.Job = &ref
		}
	}
	out := &SpoolContent{Request: req}
	if req.TemSe == "" {
		return out, fmt.Errorf("spool request %d has no TemSe object", number)
	}
	h, data, err := c.TemSeObject(ctx, req.TemSe)
	out.Header = h
	if h != nil {
		req.Storage, req.Codepage, req.Lines, req.Bytes = h.Storage, h.Codepage, h.Rows, h.Size
		out.Request = req
	}
	if err != nil {
		return out, err
	}
	if strings.EqualFold(req.DocType, "LIST") || strings.EqualFold(h.Type, "TEXT") {
		list, err := temse.DecodeList(data, h.Codepage)
		if err != nil {
			out.Raw = data
			return out, err
		}
		out.List = list
		return out, nil
	}
	out.Raw = data
	return out, nil
}

// parseSpoolTime reads TSP01's RQCRETIME: YYYYMMDDHHMMSS and two more digits.
func parseSpoolTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if len(s) < 14 {
		return time.Time{}
	}
	t, err := time.ParseInLocation("20060102150405", s[:14], time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

func decodeHexCell(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("raw column has %d hex digits", len(s))
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		v, err := strconv.ParseUint(s[2*i:2*i+2], 16, 8)
		if err != nil {
			return nil, fmt.Errorf("raw column is not hex at %d: %w", 2*i, err)
		}
		out[i] = byte(v)
	}
	return out, nil
}
