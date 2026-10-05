package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

//go:embed debug_ui.html
var debugUIAssets embed.FS

// A local face for the interactive debugger cmd/vsp/debug.go already has.
//
// Phase 1 of upstream issue #2 built the session layer this drives: a debug
// session that survives across requests, a stack, variables, stepping — all
// of it previously reachable only through that REPL's own commands. This
// serves the same underlying session over a small HTTP API and a static
// page, using exactly the two clients the REPL already uses:
//   - *adt.Client's Debugger* methods (pkg/adt/debugger.go) for the session
//     itself — listen, attach, step, stack, variables, detach.
//   - *adt.DebugWebSocketClient (ZADT_VSP) for breakpoints and for
//     triggering a report/function module, exactly as debugSession's
//     setBreakpoint/runProgram/callRFC already do.
//
// It deliberately does NOT adopt upstream's own later rewrite of this
// prototype (PRs #187/#188) onto a new pkg/saprfc (classic RFC via
// github.com/oisee/open-rfc-go, a pure-Go but self-described "early"
// library, with its own parallel ADT-over-RFC debugger session). This fork
// already has a working trigger/breakpoint mechanism and does not need a
// second one; see CLAUDE.md for the scope decision.
//
// No DAP layer, no framework, no build step: the page is //go:embed-ed into
// the binary and loads nothing from anywhere, so this works on a laptop
// behind a proxy with no CDN reachable.
var debugUICmd = &cobra.Command{
	Use:   "ui",
	Short: "Serve a local debugger UI on localhost",
	Long: `Serve a local web UI for the ABAP debugger.

It binds to localhost only, drives the same debugger session
'vsp debug' drives, and holds one session at a time.

  vsp debug ui                 # http://127.0.0.1:7799
  vsp debug ui --port 8080
  vsp debug ui --user DEVELOPER   # listen for that user's processes
  vsp -s a4h debug ui             # against a named system

Name an object and a line, press "Set breakpoint", then either "Listen only"
(something else triggers it — SE38, a job, another session) or "Run report" /
"Run RFC", which set a breakpoint if this session has none, call the target,
and wait to catch it — all in one request.

Reports with a mandatory unfilled selection-screen field are known to hang
"Run report" (SUBMIT has no escape hatch inside the stateful WebSocket the
trigger runs over — see CLAUDE.md's RUN_REPORT entry); a browser has no
Ctrl-C, so prefer "Listen only" plus triggering that report yourself for
anything with a selection screen.`,
	RunE: runDebugUI,
}

var (
	debugUIPort int
	debugUIUser string
)

func init() {
	debugUICmd.Flags().IntVar(&debugUIPort, "port", 7799, "Port to bind the UI on localhost")
	debugUICmd.Flags().StringVar(&debugUIUser, "user", "", "Whose debuggees to listen for (default: the logon user)")
	debugCmd.AddCommand(debugUICmd)
}

// debugUIServer owns the one session the page talks to. A debug session is
// single-threaded on the SAP side, so this is deliberately one session and a
// mutex, not a pool — the same "one debugSession" shape cmd/vsp/debug.go's
// REPL already uses.
type debugUIServer struct {
	client   *adt.Client
	wsClient *adt.DebugWebSocketClient
	user     string
	// readOnly blocks only the handlers that cause a new SAP-side action —
	// setting a breakpoint, or triggering a report/RFC call. It does not
	// block stepping, navigating the stack, or detaching: those apply to a
	// debuggee this session (or someone else's Listen) is already attached
	// to, and blocking them would leave a read-only user permanently stuck
	// attached with no way out. Debugger operations never flow through
	// pkg/adt/safety.go's checkMutation (there is no object URL/package to
	// check), so this is a bespoke, narrower guard — not that mechanism.
	readOnly bool

	mu         sync.Mutex
	busy       bool // a Listen or Run is in flight; refuse a second one
	attached   bool
	debuggeeID string
}

type uiState struct {
	Attached  bool                `json:"attached"`
	Busy      bool                `json:"busy"`
	Stack     *adt.DebugStackInfo `json:"stack,omitempty"`
	Variables []adt.DebugVariable `json:"variables,omitempty"`
	Note      string              `json:"note,omitempty"`
}

func runDebugUI(cmd *cobra.Command, args []string) error {
	params, err := resolveSystemParams(cmd)
	if err != nil {
		return err
	}

	client, err := getClient(params)
	if err != nil {
		return err
	}

	user := debugUIUser
	if user == "" {
		user = params.User
	}
	adt.SetTerminalIDUser(user)

	ctx := cmd.Context()

	// Unlike the REPL, which falls back to HTTP-only mode when ZADT_VSP is
	// unavailable, this command requires it: breakpoints and triggering both
	// go through it, and a page that can only listen/step/read is not "show
	// me" — it's a partial answer to a question this prototype exists to
	// answer honestly.
	wsClient := adt.NewDebugWebSocketClient(params.URL, params.Client, params.User, params.Password, params.Insecure)
	if err := wsClient.Connect(ctx); err != nil {
		return fmt.Errorf("connecting to ZADT_VSP WebSocket (required for breakpoints and triggering): %w", err)
	}
	defer wsClient.Close()

	srv := &debugUIServer{
		client:   client,
		wsClient: wsClient,
		user:     user,
		readOnly: client.Safety().ReadOnly,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleIndex)
	mux.HandleFunc("/api/state", srv.handleState)
	mux.HandleFunc("/api/bp", srv.handleBreakpoint)
	mux.HandleFunc("/api/listen", srv.handleListen)
	mux.HandleFunc("/api/run/report", srv.handleRunReport)
	mux.HandleFunc("/api/run/rfc", srv.handleRunRFC)
	mux.HandleFunc("/api/step", srv.handleStep)
	mux.HandleFunc("/api/goto", srv.handleGoTo)
	mux.HandleFunc("/api/source", srv.handleSource)
	mux.HandleFunc("/api/detach", srv.handleDetach)

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(debugUIPort))
	lc := &net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("binding %s: %w", addr, err)
	}

	fmt.Fprintf(os.Stderr, "debugger UI on http://%s (user %s)\n", addr, user)
	if srv.readOnly {
		fmt.Fprintf(os.Stderr, "read-only: breakpoints and triggering are disabled\n")
	}
	fmt.Fprintf(os.Stderr, "set a breakpoint, then Run or Listen\n")

	return (&http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}).Serve(ln)
}

func (s *debugUIServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	page, err := debugUIAssets.ReadFile("debug_ui.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func queryInt(r *http.Request, name string, def int) int {
	if n, err := strconv.Atoi(r.URL.Query().Get(name)); err == nil && n > 0 {
		return n
	}
	return def
}

// snapshot builds the whole picture the page renders from. A view of a
// stopped process is either complete or explains why it isn't — a partial
// answer with no note is worse than a slow one.
func (s *debugUIServer) snapshot(ctx context.Context, note string) *uiState {
	s.mu.Lock()
	attached := s.attached
	busy := s.busy
	s.mu.Unlock()

	st := &uiState{Attached: attached, Busy: busy, Note: note}
	if !attached {
		if st.Note == "" {
			st.Note = "not attached — set a breakpoint, then Run or Listen"
		}
		return st
	}

	stack, err := s.client.DebuggerGetStack(ctx, true)
	if err != nil {
		if st.Note == "" {
			st.Note = fmt.Sprintf("stack unavailable: %v", err)
		}
		return st
	}
	st.Stack = stack

	childVars, err := s.client.DebuggerGetChildVariables(ctx, []string{"@ROOT"})
	if err != nil {
		// A missing stack is fatal to the view; missing variables are not.
		if st.Note == "" {
			st.Note = fmt.Sprintf("variables unavailable: %v", err)
		}
		return st
	}
	if childVars != nil {
		st.Variables = childVars.Variables
	}
	return st
}

func (s *debugUIServer) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.snapshot(r.Context(), ""))
}

// tryStart claims the session for a Listen or Run, refusing a second
// concurrent one — SAP's own debug session is single-threaded per debuggee
// anyway, and two in-flight long-polls from two browser tabs would only
// race each other for nothing.
func (s *debugUIServer) tryStart() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return false
	}
	s.busy = true
	return true
}

func (s *debugUIServer) finish() {
	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()
}

// maxListenSeconds bounds how long a DebuggerListen call is allowed to ask
// SAP to hold the long-poll open for. It must stay under the ADT client's
// own http.Client.Timeout (60s, pkg/adt/config.go's Config.Timeout default)
// — that Timeout is an absolute per-request cutoff Go enforces regardless of
// any context deadline or the timeout query param DebuggerListen sends SAP,
// so a TimeoutSeconds at or above it always loses the race: SAP is still
// legitimately waiting when the client already gave up, which surfaces as
// "Client.Timeout exceeded while awaiting headers" (confirmed live) instead
// of the clean "nobody stopped within Ns" DebuggerListen is meant to return.
// cmd/vsp/debug.go's runProgram hit the same ceiling first and already backs
// off to 30s for its own trigger-and-catch call; this keeps the same margin.
const maxListenSeconds = 45

// listen blocks up to seconds waiting for a debuggee, then attaches. It runs
// unlocked except for the brief final state mutation, so it can be driven
// from a goroutine that races a trigger call (see triggerAndCatch) without
// holding s.mu across the whole wait.
func (s *debugUIServer) listen(ctx context.Context, seconds int) (string, error) {
	res, err := s.client.DebuggerListen(ctx, &adt.ListenOptions{
		DebuggingMode:  adt.DebuggingModeUser,
		User:           s.user,
		TimeoutSeconds: seconds,
	})
	if err != nil {
		return "", fmt.Errorf("listen failed: %w", err)
	}
	if res.Conflict != nil {
		return "", fmt.Errorf("listener conflict: %s (%s)", res.Conflict.ConflictText, res.Conflict.IdeUser)
	}
	if res.TimedOut || res.Debuggee == nil {
		return fmt.Sprintf("nobody stopped within %ds — is the breakpoint on an executable line, and did you run the ABAP?", seconds), nil
	}

	if _, err := s.client.DebuggerAttach(ctx, res.Debuggee.ID, s.user); err != nil {
		return "", fmt.Errorf("attach failed: %w", err)
	}

	s.mu.Lock()
	s.attached = true
	s.debuggeeID = res.Debuggee.ID
	s.mu.Unlock()

	return fmt.Sprintf("stopped at %s:%d", res.Debuggee.Program, res.Debuggee.Line), nil
}

func (s *debugUIServer) handleListen(w http.ResponseWriter, r *http.Request) {
	if !s.tryStart() {
		writeJSONStatus(w, http.StatusConflict, s.snapshot(r.Context(), "a listen or run is already in progress"))
		return
	}
	defer s.finish()

	note, err := s.listen(r.Context(), queryInt(r, "seconds", maxListenSeconds))
	if err != nil {
		note = err.Error()
	}
	writeJSON(w, s.snapshot(r.Context(), note))
}

// ensureBreakpoint sets a breakpoint only when this session has none yet —
// mirrors cmd/vsp/debug.go's runProgram, which never silently adds a second
// one for a caller who set two deliberately.
//
// A GetBreakpoints error is treated the same as "none exist" rather than
// aborting: it and SetLineBreakpoint share the same WS transport, so a
// connection problem serious enough to matter almost always fails the
// SetLineBreakpoint call right after too, and that failure is what reaches
// the user (as "breakpoint refused"). Failing open here, not the read, keeps
// a transient GetBreakpoints hiccup from blocking a Run it would not
// otherwise have blocked.
func (s *debugUIServer) ensureBreakpoint(ctx context.Context, object string, line int) error {
	if existing, err := s.wsClient.GetBreakpoints(ctx); err == nil && len(existing) > 0 {
		return nil
	}
	_, err := s.wsClient.SetLineBreakpoint(ctx, object, line)
	return err
}

// triggerAndCatch starts a listener, triggers the target on what is a
// logically separate SAP-side call so the listener is not blocked waiting on
// its own request, and waits for either to finish.
//
// The order is not obvious and getting it backwards is the natural mistake:
// listening first and calling second is the only order that cannot deadlock
// — calling first would work too, since a debuggee parks at the breakpoint
// and waits to be collected, but there is no reason to risk the other order.
// The listener runs on its own cancellable context, detached from the
// request's (so an aborted browser fetch does not tear it down while it
// might still catch something) but cancelled by this function itself on
// every exit path below — including a fast trigger failure. Without that,
// the caller's deferred finish() would release the "a listen/run is already
// in progress" guard while this goroutine was still live against SAP, and a
// retry could start a second, fully concurrent listener on the same debug
// session. So every return waits for the goroutine to actually stop.
func (s *debugUIServer) triggerAndCatch(ctx context.Context, seconds int, trigger func(context.Context) error, label string) (string, error) {
	type result struct {
		note string
		err  error
	}
	resultCh := make(chan result, 1)

	listenCtx, cancelListen := context.WithCancel(context.Background())
	defer cancelListen()

	go func() {
		note, err := s.listen(listenCtx, seconds)
		resultCh <- result{note, err}
	}()

	// Give the listener a moment to register before triggering — the same
	// margin cmd/vsp/debug.go's runProgram already uses.
	time.Sleep(100 * time.Millisecond)

	if err := trigger(ctx); err != nil {
		cancelListen()
		<-resultCh // wait for the goroutine to actually stop before busy is released
		return "", fmt.Errorf("triggering %s: %w", label, err)
	}

	select {
	case res := <-resultCh:
		if res.err != nil {
			return "", fmt.Errorf("called %s, but %w", label, res.err)
		}
		return fmt.Sprintf("called %s — %s", label, res.note), nil
	case <-time.After(time.Duration(seconds+10) * time.Second):
		cancelListen()
		<-resultCh
		return "", fmt.Errorf("called %s, but timed out waiting to catch it", label)
	}
}

// runRefusal returns why a Run (a report or an RFC call) is not allowed, or ""
// if it is. The read-only flag keeps its own message; beyond it the call also
// has to pass the resolved safety config, which is how --allowed-ops /
// --disallowed-ops reach this server. A nil client (some tests) skips the
// second check.
func (s *debugUIServer) runRefusal(op, readOnlyNote string) string {
	if s.readOnly {
		return readOnlyNote
	}
	if s.client != nil {
		if err := s.client.Safety().CheckOperation(adt.OpWorkflow, op); err != nil {
			return err.Error()
		}
	}
	return ""
}

func (s *debugUIServer) handleBreakpoint(w http.ResponseWriter, r *http.Request) {
	if s.readOnly {
		writeJSONStatus(w, http.StatusForbidden, s.snapshot(r.Context(), "read-only mode: cannot set a breakpoint"))
		return
	}

	object := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("object")))
	if object == "" {
		writeJSON(w, s.snapshot(r.Context(), "name an object first"))
		return
	}
	line := queryInt(r, "line", 1)

	id, err := s.wsClient.SetLineBreakpoint(r.Context(), object, line)
	if err != nil {
		writeJSON(w, s.snapshot(r.Context(), fmt.Sprintf("breakpoint refused: %v", err)))
		return
	}
	writeJSON(w, s.snapshot(r.Context(), fmt.Sprintf("breakpoint %s set at %s:%d", id, object, line)))
}

// handleRunReport sets a breakpoint if needed, calls the report over the
// WebSocket report domain (SUBMIT), and waits to catch it. See the RunReport
// doc comment above and CLAUDE.md's RUN_REPORT entry for the known hang risk
// on a report with a mandatory unfilled selection-screen field.
func (s *debugUIServer) handleRunReport(w http.ResponseWriter, r *http.Request) {
	if note := s.runRefusal("RunReport", "read-only mode: cannot run a report"); note != "" {
		writeJSONStatus(w, http.StatusForbidden, s.snapshot(r.Context(), note))
		return
	}
	if !s.tryStart() {
		writeJSONStatus(w, http.StatusConflict, s.snapshot(r.Context(), "a listen or run is already in progress"))
		return
	}
	defer s.finish()

	object := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("object")))
	if object == "" {
		writeJSON(w, s.snapshot(r.Context(), "name an object first"))
		return
	}
	line := queryInt(r, "line", 1)
	variant := strings.TrimSpace(r.URL.Query().Get("variant"))

	if err := s.ensureBreakpoint(r.Context(), object, line); err != nil {
		writeJSON(w, s.snapshot(r.Context(), fmt.Sprintf("breakpoint refused: %v", err)))
		return
	}

	note, err := s.triggerAndCatch(r.Context(), 30, func(ctx context.Context) error {
		return s.wsClient.RunReport(ctx, object, variant)
	}, object)
	if err != nil {
		note = err.Error()
	}
	writeJSON(w, s.snapshot(r.Context(), note))
}

// handleRunRFC sets a breakpoint if needed, calls the function module, and
// waits to catch it. Unlike RunReport this trigger has no known hang risk
// (issue #151 / CLAUDE.md 2y). v1 calls with no parameters — enough to hit a
// breakpoint inside the FM's own code; a param-taking call is a possible
// follow-up, not built here to keep this port's scope small.
func (s *debugUIServer) handleRunRFC(w http.ResponseWriter, r *http.Request) {
	if note := s.runRefusal("CallRFC", "read-only mode: cannot call an RFC"); note != "" {
		writeJSONStatus(w, http.StatusForbidden, s.snapshot(r.Context(), note))
		return
	}
	if !s.tryStart() {
		writeJSONStatus(w, http.StatusConflict, s.snapshot(r.Context(), "a listen or run is already in progress"))
		return
	}
	defer s.finish()

	function := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("function")))
	if function == "" {
		writeJSON(w, s.snapshot(r.Context(), "name a function module first"))
		return
	}
	line := queryInt(r, "line", 1)

	if err := s.ensureBreakpoint(r.Context(), function, line); err != nil {
		writeJSON(w, s.snapshot(r.Context(), fmt.Sprintf("breakpoint refused: %v", err)))
		return
	}

	note, err := s.triggerAndCatch(r.Context(), 30, func(ctx context.Context) error {
		_, err := s.wsClient.CallRFC(ctx, function, nil)
		return err
	}, function)
	if err != nil {
		note = err.Error()
	}
	writeJSON(w, s.snapshot(r.Context(), note))
}

func (s *debugUIServer) handleStep(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	attached := s.attached
	s.mu.Unlock()
	if !attached {
		writeJSON(w, s.snapshot(r.Context(), "not attached"))
		return
	}

	stepType, ok := map[string]adt.DebugStepType{
		"into":     adt.DebugStepInto,
		"over":     adt.DebugStepOver,
		"out":      adt.DebugStepReturn,
		"continue": adt.DebugStepContinue,
	}[strings.ToLower(r.URL.Query().Get("type"))]
	if !ok {
		http.Error(w, "step kinds: into, over, out, continue", http.StatusBadRequest)
		return
	}

	result, err := s.client.DebuggerStep(r.Context(), stepType, "")
	if err != nil {
		// Continue ends the session when nothing else is hit, and that is a
		// normal outcome rather than a failure.
		s.mu.Lock()
		s.attached = false
		s.debuggeeID = ""
		s.mu.Unlock()
		writeJSON(w, s.snapshot(r.Context(), fmt.Sprintf("session ended after %s: %v", stepType, err)))
		return
	}
	if !result.IsSteppingPossible {
		s.mu.Lock()
		s.attached = false
		s.debuggeeID = ""
		s.mu.Unlock()
		writeJSON(w, s.snapshot(r.Context(), "debuggee terminated"))
		return
	}
	writeJSON(w, s.snapshot(r.Context(), ""))
}

func (s *debugUIServer) handleGoTo(w http.ResponseWriter, r *http.Request) {
	uri := r.URL.Query().Get("uri")
	s.mu.Lock()
	attached := s.attached
	s.mu.Unlock()
	if uri == "" || !attached {
		writeJSON(w, s.snapshot(r.Context(), ""))
		return
	}
	if err := s.client.DebuggerGoToStack(r.Context(), uri); err != nil {
		writeJSON(w, s.snapshot(r.Context(), fmt.Sprintf("goto failed: %v", err)))
		return
	}
	writeJSON(w, s.snapshot(r.Context(), ""))
}

// handleSource fetches the source of the frame being shown, named by the
// page from the stack entry it already has (see /api/state) — program and
// include, exactly as the debugger's own stack entries name them. An include
// is fetched as an include; anything else is read as a program.
func (s *debugUIServer) handleSource(w http.ResponseWriter, r *http.Request) {
	program := r.URL.Query().Get("program")
	include := r.URL.Query().Get("include")

	name, src, err := program, "", error(nil)
	if include != "" && include != program {
		name = include
		src, err = s.client.GetInclude(r.Context(), include)
	} else if program != "" {
		src, err = s.client.GetProgram(r.Context(), program)
	} else {
		writeJSON(w, map[string]string{"source": "", "name": ""})
		return
	}
	if err != nil {
		writeJSON(w, map[string]string{"source": "", "name": name, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"source": src, "name": name})
}

func (s *debugUIServer) handleDetach(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	attached := s.attached
	s.mu.Unlock()

	if attached {
		_ = s.client.DebuggerDetach(r.Context())
	}

	s.mu.Lock()
	s.attached = false
	s.debuggeeID = ""
	s.mu.Unlock()

	writeJSON(w, s.snapshot(r.Context(), "detached"))
}
