package adt

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Background jobs are two tables: TBTCO, one row per job with its status and
// times, and TBTCP, one row per step with the program, the variant and the spool
// the step wrote. The job log is a TemSe object that most systems keep in files,
// so it is read over RFC (pkg/saprfc), not here.
//
// Neither table has a client field the data preview filters on: TBTCO keeps the
// client in AUTHCKMAN and TBTCP has none, so the listing restricts TBTCO to the
// logon client and reaches TBTCP only through jobs found there.

const (
	jobsDefaultLimit  = 50
	jobsMaxLimit      = 200
	jobsDefaultWindow = 7 * 24 * time.Hour
	jobsStepBlock     = 50 // jobcount values per steps query
)

var (
	jobUserRe    = regexp.MustCompile(`^[A-Z0-9_]{1,12}$`)
	jobProgramRe = regexp.MustCompile(`^[A-Z0-9_/]{1,40}$`)
	jobClientRe  = regexp.MustCompile(`^[0-9]{3}$`)
)

// Job is one TBTCO row with its steps.
type Job struct {
	Name   string `json:"name"`
	Count  string `json:"count"`
	Status string `json:"status"`
	// StatusText spells the one-letter status out.
	StatusText string    `json:"statusText"`
	Scheduled  time.Time `json:"scheduled,omitempty"`
	Released   time.Time `json:"released,omitempty"`
	Started    time.Time `json:"started,omitempty"`
	Ended      time.Time `json:"ended,omitempty"`
	// Duration is Ended minus Started for a job that ran.
	Duration string `json:"duration,omitempty"`
	User     string `json:"user,omitempty"`
	Server   string `json:"server,omitempty"`
	Periodic bool   `json:"periodic,omitempty"`
	Class    string `json:"class,omitempty"`
	// Log is the TemSe name of the job log.
	Log   string    `json:"log,omitempty"`
	Steps []JobStep `json:"steps,omitempty"`
}

// JobStep is one TBTCP row.
type JobStep struct {
	Step    int    `json:"step"`
	Program string `json:"program,omitempty"`
	Variant string `json:"variant,omitempty"`
	User    string `json:"user,omitempty"`
	Lang    string `json:"language,omitempty"`
	Status  string `json:"status,omitempty"`
	// Spool is the number of the spool request the step wrote, 0 for none.
	Spool int `json:"spool,omitempty"`
	// External is an external command or program step.
	External string `json:"external,omitempty"`
}

// JobFilter narrows a listing. Every text value is checked against a whitelist
// before it reaches SQL (the data preview has no bind parameters).
type JobFilter struct {
	Name    string // exact, or a pattern with * and + wildcards
	User    string
	Status  string // letters from P S Y R F A Z, with or without separators
	Program string // only jobs with a step running this program
	From    time.Time
	To      time.Time
	Limit   int // default 50, at most 200
}

// SAPClient is the logon client the connection was configured with.
func (c *Client) SAPClient() string { return c.config.Client }

// Jobs lists background jobs of the logon client, latest start first, with
// their steps. Without any filter it covers the last seven days.
func (c *Client) Jobs(ctx context.Context, filter JobFilter) ([]Job, error) {
	if err := c.checkSafety(OpRead, "Jobs"); err != nil {
		return nil, err
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = jobsDefaultLimit
	}
	if limit > jobsMaxLimit {
		limit = jobsMaxLimit
	}
	client := strings.TrimSpace(c.SAPClient())
	if !jobClientRe.MatchString(client) {
		return nil, fmt.Errorf("jobs: the logon client %q is not a 3-digit client", client)
	}

	var terms []string
	var nameGlob *regexp.Regexp
	unfiltered := true
	if v := strings.ToUpper(strings.TrimSpace(filter.Name)); v != "" {
		unfiltered = false
		if !ValidJobNamePattern(v) {
			return nil, fmt.Errorf("invalid job name %q: expected up to 32 letters, digits, or _ - / with * or + wildcards", filter.Name)
		}
		if strings.ContainsAny(v, "*+") {
			like := strings.NewReplacer("*", "%", "+", "_").Replace(v)
			terms = append(terms, "jobname LIKE '"+like+"'")
			// A real "_" in the name is a wildcard to LIKE; the glob makes it exact.
			nameGlob = jobGlob(v)
		} else {
			terms = append(terms, "jobname = '"+v+"'")
		}
	}
	if v := strings.ToUpper(strings.TrimSpace(filter.User)); v != "" {
		unfiltered = false
		if !jobUserRe.MatchString(v) {
			return nil, fmt.Errorf("invalid user %q: expected up to 12 letters, digits or _", filter.User)
		}
		terms = append(terms, "sdluname = '"+v+"'")
	}
	if v := strings.ToUpper(strings.TrimSpace(filter.Status)); v != "" {
		unfiltered = false
		var codes []string
		for _, ch := range v {
			switch {
			case ch == ',' || ch == ' ':
			case strings.ContainsRune("PSYRFAZ", ch):
				codes = append(codes, "'"+string(ch)+"'")
			default:
				return nil, fmt.Errorf("invalid status %q: use letters from P S Y R F A Z", filter.Status)
			}
		}
		if len(codes) == 0 {
			return nil, fmt.Errorf("invalid status %q: use letters from P S Y R F A Z", filter.Status)
		}
		terms = append(terms, "status IN ( "+strings.Join(codes, ", ")+" )")
	}
	program := strings.ToUpper(strings.TrimSpace(filter.Program))
	if program != "" {
		unfiltered = false
		if !jobProgramRe.MatchString(program) {
			return nil, fmt.Errorf("invalid program %q: expected up to 40 letters, digits, _ or /", filter.Program)
		}
	}
	from, to := filter.From, filter.To
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		return nil, fmt.Errorf("invalid period: from %s is after to %s", from.Format("2006-01-02"), to.Format("2006-01-02"))
	}
	if unfiltered && from.IsZero() && to.IsZero() {
		from = time.Now().Add(-jobsDefaultWindow)
	}
	if !from.IsZero() || !to.IsZero() {
		low, high := "00000000", "99991231"
		if !from.IsZero() {
			low = from.Format("20060102")
		}
		if !to.IsZero() {
			high = to.Format("20060102")
		}
		terms = append(terms, "sdldate BETWEEN '"+low+"' AND '"+high+"'")
	}

	var wanted map[string]bool
	if program != "" {
		pairs, err := c.jobsRunningProgram(ctx, program, from, to, limit)
		if err != nil {
			return nil, err
		}
		if len(pairs) == 0 {
			return []Job{}, nil
		}
		wanted = map[string]bool{}
		var names, counts []string
		for _, p := range pairs {
			wanted[p] = true
			name, count, _ := strings.Cut(p, "\x00")
			names = append(names, "'"+name+"'")
			counts = append(counts, "'"+count+"'")
		}
		terms = append(terms, "jobname IN ( "+strings.Join(uniqueJobStrings(names), ", ")+" )")
		terms = append(terms, "jobcount IN ( "+strings.Join(uniqueJobStrings(counts), ", ")+" )")
	}

	query := "SELECT jobname, jobcount, status, sdldate, sdltime, reldate, reltime, strtdate, strttime, enddate, endtime, sdluname, reaxserver, execserver, periodic, jobclass, joblog FROM tbtco WHERE authckman = '" + client + "'"
	if len(terms) > 0 {
		query += " AND " + strings.Join(terms, " AND ")
	}
	query += " ORDER BY strtdate DESCENDING, strttime DESCENDING, sdldate DESCENDING, sdltime DESCENDING"
	fetch := limit
	if wanted != nil || nameGlob != nil {
		// A Go-side filter follows, so SQL must not cut the list first.
		fetch = max(limit*4, len(wanted))
	}
	res, err := c.RunQuery(ctx, query, fetch)
	if err != nil {
		return nil, fmt.Errorf("reading jobs: %w", err)
	}
	jobs := []Job{}
	if res == nil {
		return jobs, nil
	}
	for _, row := range res.Rows {
		j := Job{
			Name: jobCell(row, "JOBNAME"), Count: jobCell(row, "JOBCOUNT"), Status: jobCell(row, "STATUS"),
			User: jobCell(row, "SDLUNAME"), Class: jobCell(row, "JOBCLASS"), Log: jobCell(row, "JOBLOG"),
			Periodic: jobCell(row, "PERIODIC") == "X",
		}
		if wanted != nil && !wanted[j.Name+"\x00"+j.Count] {
			continue
		}
		if nameGlob != nil && !nameGlob.MatchString(j.Name) {
			continue
		}
		if len(jobs) >= limit {
			break
		}
		j.StatusText = JobStatusText(j.Status)
		if j.Server = jobCell(row, "EXECSERVER"); j.Server == "" {
			j.Server = jobCell(row, "REAXSERVER")
		}
		j.Scheduled = parseSAPStamp(jobCell(row, "SDLDATE"), jobCell(row, "SDLTIME"))
		j.Released = parseSAPStamp(jobCell(row, "RELDATE"), jobCell(row, "RELTIME"))
		j.Started = parseSAPStamp(jobCell(row, "STRTDATE"), jobCell(row, "STRTTIME"))
		j.Ended = parseSAPStamp(jobCell(row, "ENDDATE"), jobCell(row, "ENDTIME"))
		if !j.Started.IsZero() && !j.Ended.IsZero() {
			j.Duration = j.Ended.Sub(j.Started).String()
		}
		jobs = append(jobs, j)
	}
	if len(jobs) == 0 {
		return jobs, nil
	}
	if err := c.attachJobSteps(ctx, jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

// jobsRunningProgram finds the jobs that have a step running program: TBTCP is
// read first because it carries the program, TBTCO afterwards for the rest.
func (c *Client) jobsRunningProgram(ctx context.Context, program string, from, to time.Time, limit int) ([]string, error) {
	query := "SELECT jobname, jobcount FROM tbtcp WHERE progname = '" + program + "'"
	if !from.IsZero() || !to.IsZero() {
		low, high := "00000000", "99991231"
		if !from.IsZero() {
			low = from.Format("20060102")
		}
		if !to.IsZero() {
			high = to.Format("20060102")
		}
		query += " AND sdldate BETWEEN '" + low + "' AND '" + high + "'"
	}
	query += " ORDER BY sdldate DESCENDING, sdltime DESCENDING"
	res, err := c.RunQuery(ctx, query, jobsMaxLimit*5)
	if err != nil {
		return nil, fmt.Errorf("reading job steps: %w", err)
	}
	var pairs []string
	if res == nil {
		return pairs, nil
	}
	seen := map[string]bool{}
	for _, row := range res.Rows {
		name, count := jobCell(row, "JOBNAME"), jobCell(row, "JOBCOUNT")
		key := name + "\x00" + count
		if ValidJobName(name) && ValidJobCount(count) && !seen[key] {
			seen[key] = true
			pairs = append(pairs, key)
		}
	}
	return pairs, nil
}

// attachJobSteps reads the TBTCP rows of the listed jobs, a block of job counts
// at a time, and matches name and count in Go: the IN lists narrow the read,
// the pair match makes it exact.
func (c *Client) attachJobSteps(ctx context.Context, jobs []Job) error {
	index := map[string]int{}
	var counts, names []string
	for i, j := range jobs {
		if !ValidJobName(j.Name) || !ValidJobCount(j.Count) {
			continue
		}
		index[j.Name+"\x00"+j.Count] = i
		counts = append(counts, "'"+j.Count+"'")
		names = append(names, "'"+j.Name+"'")
	}
	counts, names = uniqueJobStrings(counts), uniqueJobStrings(names)
	const stepRows = jobsStepBlock * 40
	for start := 0; start < len(counts); start += jobsStepBlock {
		end := min(start+jobsStepBlock, len(counts))
		res, err := c.RunQuery(ctx, "SELECT jobname, jobcount, stepcount, progname, variant, authcknam, language, status, listident, xpgprog, extcmd FROM tbtcp WHERE jobname IN ( "+strings.Join(names, ", ")+" ) AND jobcount IN ( "+strings.Join(counts[start:end], ", ")+" ) ORDER BY jobname, jobcount, stepcount", stepRows)
		if err != nil {
			return fmt.Errorf("reading job steps: %w", err)
		}
		if res == nil {
			continue
		}
		if len(res.Rows) >= stepRows {
			return fmt.Errorf("reading job steps: more than %d step rows for %d jobs; narrow the filter or lower the limit", stepRows, end-start)
		}
		for _, row := range res.Rows {
			i, ok := index[jobCell(row, "JOBNAME")+"\x00"+jobCell(row, "JOBCOUNT")]
			if !ok {
				continue
			}
			s := JobStep{
				Program: jobCell(row, "PROGNAME"), Variant: jobCell(row, "VARIANT"), User: jobCell(row, "AUTHCKNAM"),
				Lang: jobCell(row, "LANGUAGE"), Status: jobCell(row, "STATUS"),
			}
			s.Step, _ = strconv.Atoi(jobCell(row, "STEPCOUNT"))
			s.Spool, _ = strconv.Atoi(jobCell(row, "LISTIDENT"))
			if s.Program == "" {
				s.External = strings.TrimSpace(jobCell(row, "EXTCMD") + " " + jobCell(row, "XPGPROG"))
			}
			jobs[i].Steps = append(jobs[i].Steps, s)
		}
	}
	return nil
}

// jobCell reads a result cell as trimmed text; numeric cells arrive padded.
func jobCell(row map[string]interface{}, key string) string {
	switch v := row[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

// parseSAPStamp turns a DATS and a TIMS value into a time; an initial date
// ("00000000") gives the zero time.
func parseSAPStamp(date, clock string) time.Time {
	date, clock = strings.TrimSpace(date), strings.TrimSpace(clock)
	if len(date) != 8 || date == "00000000" {
		return time.Time{}
	}
	if len(clock) != 6 {
		clock = "000000"
	}
	t, err := time.ParseInLocation("20060102150405", date+clock, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

func uniqueJobStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// jobGlob turns a job-name pattern (* any run, + one character) into an anchored
// regular expression.
func jobGlob(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '+':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}
