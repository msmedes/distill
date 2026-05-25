package distill

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed templates/*.html
var templatesFS embed.FS

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.Int("port", 7373, "port to listen on")
	host := fs.String("host", "127.0.0.1", "host to bind to")
	if err := fs.Parse(args); err != nil {
		return err
	}

	p, err := resolvePaths()
	if err != nil {
		return err
	}

	srv := &server{paths: p}
	if err := srv.loadTemplates(); err != nil {
		return fmt.Errorf("loading templates: %w", err)
	}
	if prefs, err := readPreferences(p); err == nil {
		startSessionIndexRefresh(p, prefs.watchProduct())
	} else {
		log.Printf("session index refresh skipped: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleIndex)
	mux.HandleFunc("/help", srv.handleHelp)
	mux.HandleFunc("/sessions", srv.handleSessions)
	mux.HandleFunc("/obs/", srv.handleObsAction)
	mux.HandleFunc("/run", srv.handleRun)
	mux.HandleFunc("/synthesize", srv.handleSynthesize)
	mux.HandleFunc("/settings", srv.handleSettings)

	addr := net.JoinHostPort(*host, fmt.Sprint(*port))
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		fmt.Printf("distill serving on http://%s\n", addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	<-ctx.Done()
	fmt.Println("\nshutting down…")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

type server struct {
	paths        paths
	indexTmpl    *template.Template
	helpTmpl     *template.Template
	sessionsTmpl *template.Template
	settingsTmpl *template.Template
	previewTmpl  *template.Template
}

func (s *server) loadTemplates() error {
	funcs := template.FuncMap{
		"shortID": func(id string) string {
			if len(id) > 8 {
				return id[:8]
			}
			return id
		},
		"shortTime": func(ts string) string {
			t, err := time.Parse(time.RFC3339, ts)
			if err != nil {
				return ts
			}
			return t.Local().Format("2006-01-02 15:04")
		},
		"join": strings.Join,
		"productsFor": func(evidence []evidence) []product {
			seen := map[product]bool{}
			var out []product
			for _, e := range evidence {
				p := e.Product
				if p == "" {
					p = productClaude
				}
				if seen[p] {
					continue
				}
				seen[p] = true
				out = append(out, p)
			}
			return out
		},
		"scopeLabel": func(o observation) string {
			normalizeObservation(&o)
			if o.Scope == scopeProject {
				if o.ProjectCWD == "" {
					return "project"
				}
				return "project: " + filepath.Base(o.ProjectCWD)
			}
			return "user"
		},
		"proposalLabel": func(p proposal) string {
			normalizeProposal(&p, scopeUser)
			scope := "user"
			if p.Scope == scopeProject {
				scope = "project"
			}
			switch p.Artifact {
			case artifactSkill:
				return scope + " skill proposal"
			case artifactAgentsMD:
				return scope + " instructions proposal"
			default:
				return scope + " proposal"
			}
		},
	}
	tmpl, err := template.New("observations.html").Funcs(funcs).
		ParseFS(templatesFS, "templates/observations.html")
	if err != nil {
		return err
	}
	s.indexTmpl = tmpl
	helpTmpl, err := template.ParseFS(templatesFS, "templates/help.html")
	if err != nil {
		return err
	}
	s.helpTmpl = helpTmpl
	sessionsTmpl, err := template.New("sessions.html").Funcs(funcs).
		ParseFS(templatesFS, "templates/sessions.html")
	if err != nil {
		return err
	}
	s.sessionsTmpl = sessionsTmpl
	settingsTmpl, err := template.New("settings.html").Funcs(funcs).
		ParseFS(templatesFS, "templates/settings.html")
	if err != nil {
		return err
	}
	s.settingsTmpl = settingsTmpl
	previewTmpl, err := template.ParseFS(templatesFS, "templates/preview.html")
	if err != nil {
		return err
	}
	s.previewTmpl = previewTmpl
	return nil
}

type indexData struct {
	Observations       []observation
	ObservationCount   int
	SessionsProcessed  int
	RunBatchLimit      int
	WatchProduct       product
	SessionIndexStatus string
	Filter             string
	Types              []observationType
	Preferences        preferences
}

type sessionsData struct {
	Sessions []processedSessionView
	Count    int
}

type processedSessionView struct {
	Product     product
	SessionID   string
	ShortID     string
	SessionTime string
	ProcessedAt string
	Length      string
	Project     string
	Title       string
	Command     string
	SourcePath  string
}

type settingsData struct {
	Preferences        preferences
	WatchProduct       product
	ExtractionBackend  string
	ClaudeCommand      string
	CodexCommand       string
	IndexedClaude      int
	IndexedCodex       int
	ProcessedClaude    int
	ProcessedCodex     int
	UnprocessedClaude  int
	UnprocessedCodex   int
	SessionIndexStatus string
	LastWatcherError   string
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	st := newStore(s.paths)
	obs, err := st.readObservations()
	if err != nil {
		http.Error(w, fmt.Sprintf("reading observations: %v", err), http.StatusInternalServerError)
		return
	}

	filter := r.URL.Query().Get("type")
	filtered := obs
	if filter != "" {
		filtered = nil
		for _, o := range obs {
			if string(o.Type) == filter {
				filtered = append(filtered, o)
			}
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		a, b := statusRank(filtered[i].Status), statusRank(filtered[j].Status)
		if a != b {
			return a < b
		}
		if filtered[i].EvidenceCount != filtered[j].EvidenceCount {
			return filtered[i].EvidenceCount > filtered[j].EvidenceCount
		}
		return filtered[i].LastSeen > filtered[j].LastSeen
	})

	state, err := st.readState()
	if err != nil {
		http.Error(w, fmt.Sprintf("reading state: %v", err), http.StatusInternalServerError)
		return
	}
	sessionsProcessed := len(state.ProcessedSessions)
	prefs, err := readPreferences(s.paths)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading preferences: %v", err), http.StatusInternalServerError)
		return
	}
	watchProduct := prefs.watchProduct()
	indexStatus, err := explainSessionIndex(s.paths)
	if err != nil {
		indexStatus = "index unavailable"
	}

	data := indexData{
		Observations:       filtered,
		ObservationCount:   len(obs),
		SessionsProcessed:  sessionsProcessed,
		RunBatchLimit:      prefs.WebRunBatchLimit,
		WatchProduct:       watchProduct,
		SessionIndexStatus: indexStatus,
		Filter:             filter,
		Types: []observationType{
			typePreference, typeWorkflow, typeFriction, typeToolUse,
		},
		Preferences: prefs,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.indexTmpl.Execute(w, data); err != nil {
		log.Printf("template execute: %v", err)
	}
}

func (s *server) handleHelp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != "/help" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.helpTmpl.Execute(w, nil); err != nil {
		log.Printf("help template execute: %v", err)
	}
}

func (s *server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != "/sessions" {
		http.NotFound(w, r)
		return
	}
	sessions, err := s.processedSessions()
	if err != nil {
		http.Error(w, fmt.Sprintf("reading sessions: %v", err), http.StatusInternalServerError)
		return
	}
	data := sessionsData{
		Sessions: sessions,
		Count:    len(sessions),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.sessionsTmpl.Execute(w, data); err != nil {
		log.Printf("sessions template execute: %v", err)
	}
}

func (s *server) processedSessions() ([]processedSessionView, error) {
	st := newStore(s.paths)
	state, err := st.readState()
	if err != nil {
		return nil, err
	}

	indexed, err := indexedSessionsByStateKey(s.paths)
	if err != nil {
		indexed = map[string]sessionMeta{}
	}

	views := make([]processedSessionView, 0, len(state.ProcessedSessions))
	for key, processedAt := range state.ProcessedSessions {
		productName, sessionID := splitSessionStateKey(key)
		meta, hasMeta := indexed[key]
		if !hasMeta && productName == productClaude {
			meta, hasMeta = indexed[sessionStateKey(productClaude, sessionID)]
		}
		if hasMeta {
			productName = meta.product
			sessionID = meta.sessionID
		}

		view := processedSessionView{
			Product:     productName,
			SessionID:   sessionID,
			ShortID:     shortID(sessionID),
			ProcessedAt: processedAt,
			Title:       "untitled session",
		}
		if hasMeta {
			view.SessionTime = meta.mtime.Format(time.RFC3339)
			view.Project = projectLabel(meta.cwd, meta.projectDir)
			view.SourcePath = meta.filePath
			view.Command = sessionResumeCommand(productName, sessionID, meta.cwd)
			title, length := summarizeSessionFile(meta)
			if title != "" {
				view.Title = title
			}
			view.Length = length
		}
		views = append(views, view)
	}

	sort.Slice(views, func(i, j int) bool {
		if views[i].SessionTime != views[j].SessionTime {
			if views[i].SessionTime == "" {
				return false
			}
			if views[j].SessionTime == "" {
				return true
			}
			return views[i].SessionTime > views[j].SessionTime
		}
		if views[i].ProcessedAt != views[j].ProcessedAt {
			return views[i].ProcessedAt > views[j].ProcessedAt
		}
		return views[i].SessionID > views[j].SessionID
	})
	return views, nil
}

func splitSessionStateKey(key string) (product, string) {
	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return productClaude, key
	}
	return product(parts[0]), parts[1]
}

func projectLabel(cwd, projectDir string) string {
	if cwd != "" {
		return filepath.Base(cwd)
	}
	if projectDir != "" {
		return filepath.Base(projectDir)
	}
	return ""
}

func sessionResumeCommand(p product, sessionID, cwd string) string {
	var cmd string
	switch p {
	case productClaude:
		cmd = "claude --resume " + shellQuote(sessionID)
	case productCodex:
		cmd = "codex resume " + shellQuote(sessionID)
	default:
		return ""
	}
	if cwd == "" {
		return cmd
	}
	return "cd " + shellQuote(cwd) + " && " + cmd
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func summarizeSessionFile(s sessionMeta) (string, string) {
	turns, err := parseSessionTranscript(s)
	if err != nil {
		return "", ""
	}
	var title string
	var first, last time.Time
	for _, turn := range turns {
		if title == "" && turn.role == "user" {
			title = compactTitle(turn.text)
		}
		if turn.timestamp == "" {
			continue
		}
		ts, err := time.Parse(time.RFC3339, turn.timestamp)
		if err != nil {
			continue
		}
		if first.IsZero() || ts.Before(first) {
			first = ts
		}
		if last.IsZero() || ts.After(last) {
			last = ts
		}
	}
	if first.IsZero() || last.IsZero() || !last.After(first) {
		return title, ""
	}
	return title, humanDuration(last.Sub(first))
}

func compactTitle(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	const max = 110
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return strings.TrimSpace(string(runes[:max-3])) + "..."
}

func humanDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Minute {
		return "<1m"
	}
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}

func (s *server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.renderSettings(w)
		return
	case http.MethodPost:
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	prefs, err := readPreferences(s.paths)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, fullSettings := r.Form["extraction_backend"]; fullSettings {
		prefs.WatchClaude = r.FormValue("watch_claude") == "on"
		prefs.WatchCodex = r.FormValue("watch_codex") == "on"
		prefs.AutomaticWatch = r.FormValue("automatic_watch") == "on"
		prefs.ExtractionBackend = r.FormValue("extraction_backend")
		prefs.ExtractionModel = r.FormValue("extraction_model")
		prefs.ClaudeCommandPath = r.FormValue("claude_command_path")
		prefs.CodexCommandPath = r.FormValue("codex_command_path")
		prefs.WatchInterval = r.FormValue("watch_interval")
		prefs.QuietFor = r.FormValue("quiet_for")
		prefs.WebRunBatchLimit = parsePositiveIntForm(r, "web_run_batch_limit", prefs.WebRunBatchLimit)
		prefs.MinUserTurns = parsePositiveIntForm(r, "min_user_turns", prefs.MinUserTurns)
		prefs.MinUserChars = parsePositiveIntForm(r, "min_user_chars", prefs.MinUserChars)
		prefs.MaxTranscriptChars = parsePositiveIntForm(r, "max_transcript_chars", prefs.MaxTranscriptChars)
		prefs.MaxObservations = parsePositiveIntForm(r, "max_observations", prefs.MaxObservations)
		prefs.ZoomContextChars = parsePositiveIntForm(r, "zoom_context_chars", prefs.ZoomContextChars)
		prefs.NoSkip = r.FormValue("skip_low_signal") != "on"
		prefs.PromotionMode = r.FormValue("promotion_mode")
		prefs.ClaudeMDPath = r.FormValue("claude_md_path")
		prefs.CodexAgentsPath = r.FormValue("codex_agents_path")
		prefs.ProjectInstructionsFile = r.FormValue("project_instructions_file")
		prefs.ProjectSkillsDir = r.FormValue("project_skills_dir")
	}
	prefs.AlwaysOnPath = r.FormValue("always_on_path")
	prefs.SkillsDir = r.FormValue("skills_dir")
	if err := writePreferences(s.paths, prefs); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	target := "/settings"
	if ref := r.Header.Get("Referer"); strings.HasPrefix(ref, "http://"+r.Host) || strings.HasPrefix(ref, "https://"+r.Host) {
		target = ref
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func parsePositiveIntForm(r *http.Request, name string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(r.FormValue(name)))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func (s *server) renderSettings(w http.ResponseWriter) {
	prefs, err := readPreferences(s.paths)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading preferences: %v", err), http.StatusInternalServerError)
		return
	}
	data := settingsData{
		Preferences:        prefs,
		WatchProduct:       prefs.watchProduct(),
		ExtractionBackend:  prefs.ExtractionBackend,
		ClaudeCommand:      resolvedCommandLabel("claude", prefs.ClaudeCommandPath),
		CodexCommand:       resolvedCommandLabel("codex", prefs.CodexCommandPath),
		SessionIndexStatus: "index unavailable",
		LastWatcherError:   latestWatcherError(s.paths),
	}
	data.IndexedClaude, data.IndexedCodex, data.ProcessedClaude, data.ProcessedCodex, data.UnprocessedClaude, data.UnprocessedCodex = settingsSessionCounts(s.paths)
	if status, err := explainSessionIndex(s.paths); err == nil {
		data.SessionIndexStatus = status
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.settingsTmpl.Execute(w, data); err != nil {
		log.Printf("settings template execute: %v", err)
	}
}

func resolvedCommandLabel(name, override string) string {
	if name == "claude" {
		path, _ := resolveClaudeCommand(override)
		return path
	}
	if strings.TrimSpace(override) != "" {
		path, _ := resolveCommand(name, override)
		return path
	}
	path, err := exec.LookPath(name)
	if err == nil {
		return path
	}
	if found := findExecutable(name, filepath.SplitList(extendedExecutablePath())); found != "" {
		return found
	}
	return "not found"
}

func settingsSessionCounts(p paths) (indexedClaude, indexedCodex, processedClaude, processedCodex, unprocessedClaude, unprocessedCodex int) {
	st := newStore(p)
	state, err := st.readState()
	if err == nil {
		for key := range state.ProcessedSessions {
			productName, _ := splitSessionStateKey(key)
			switch productName {
			case productClaude:
				processedClaude++
			case productCodex:
				processedCodex++
			}
		}
	}
	indexed, err := indexedSessionsByStateKey(p)
	if err != nil {
		return
	}
	for key, session := range indexed {
		processed := false
		if state != nil {
			processed = processedSession(state, session)
			if !processed && session.product == productClaude {
				_, processed = state.ProcessedSessions[key]
			}
		}
		switch session.product {
		case productClaude:
			indexedClaude++
			if !processed {
				unprocessedClaude++
			}
		case productCodex:
			indexedCodex++
			if !processed {
				unprocessedCodex++
			}
		}
	}
	return
}

func latestWatcherError(p paths) string {
	b, err := os.ReadFile(filepath.Join(p.stateDir, "watch.log"))
	if err != nil {
		return "none recorded"
	}
	lines := strings.Split(string(b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.Contains(line, "error:") || strings.Contains(line, "failed") {
			return line
		}
	}
	return "none recorded"
}

// handleObsAction dispatches POSTs of the shape /obs/{id}/{action}.
// Always replies with a redirect back to "/" so the page re-renders fresh;
// browser HTML forms can target this directly with method=POST.
func (s *server) handleObsAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "obs" {
		http.NotFound(w, r)
		return
	}
	id, action := parts[1], parts[2]

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var (
		res actionResult
		err error
	)
	switch action {
	case "ignore":
		res, err = setStatus(s.paths, id, statusIgnored)
	case "unignore":
		res, err = setStatus(s.paths, id, statusActive)
	case "note":
		res, err = addNote(s.paths, id, r.FormValue("text"))
	case "promote-claude-md":
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()
		preview, err := previewPromoteToClaudeMD(ctx, s.paths, id, true)
		if err != nil {
			log.Printf("preview action %s on %s failed: %v", action, id, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.renderPreview(w, r, preview)
		return
	case "promote-skill":
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		preview, err := previewPromoteToSkill(ctx, s.paths, id, true)
		if err != nil {
			log.Printf("preview action %s on %s failed: %v", action, id, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.renderPreview(w, r, preview)
		return
	case "accept-proposal":
		artifact := normalizeProposalArtifact(r.FormValue("artifact"), r.FormValue("kind"))
		scope := observationScope(r.FormValue("scope"))
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		preview, err := previewAcceptScopedProposal(ctx, s.paths, id, artifact, scope, true)
		if err != nil {
			log.Printf("preview action %s on %s failed: %v", action, id, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.renderPreview(w, r, preview)
		return
	case "dismiss-proposal":
		artifact := normalizeProposalArtifact(r.FormValue("artifact"), r.FormValue("kind"))
		res, err = dismissScopedProposal(s.paths, id, artifact, observationScope(r.FormValue("scope")))
	case "commit-promote-claude-md":
		res, err = commitClaudeMDPreviewWithScope(s.paths, id, r.FormValue("path"), r.FormValue("output"), r.FormValue("base_hash"), observationScope(r.FormValue("scope")))
	case "commit-promote-skill":
		res, err = commitSkillPreviewWithScope(s.paths, id, r.FormValue("path"), r.FormValue("output"), observationScope(r.FormValue("scope")))
	default:
		http.Error(w, "unknown action: "+action, http.StatusBadRequest)
		return
	}

	if err != nil {
		log.Printf("action %s on %s failed: %v", action, id, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("action %s on %s: %s", action, id, res.Message)

	// Redirect back to the referrer if it's local; otherwise root.
	target := "/"
	if ref := r.Header.Get("Referer"); strings.HasPrefix(ref, "http://"+r.Host) || strings.HasPrefix(ref, "https://"+r.Host) {
		target = ref
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// handleRun processes the newest unprocessed sessions on demand. It shares the
// extract path used by the CLI so local skip rules and processed-session state
// stay identical.
func (s *server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	prefs, err := readPreferences(s.paths)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	opts := extractOpts{
		product:           prefs.watchProduct(),
		recent:            prefs.WebRunBatchLimit,
		onlyNew:           true,
		model:             prefs.ExtractionModel,
		maxTranscriptChar: prefs.MaxTranscriptChars,
		minUserTurns:      prefs.MinUserTurns,
		minUserChars:      prefs.MinUserChars,
		maxObservations:   prefs.MaxObservations,
		zoomContextChars:  prefs.ZoomContextChars,
		noSkip:            prefs.NoSkip,
	}
	quietFor, err := time.ParseDuration(prefs.QuietFor)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := runExtractWithPaths(s.paths, opts, quietFor); err != nil {
		log.Printf("run: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleSynthesize runs a synthesis pass on demand. Blocks until the LLM call
// returns; v1 accepts that. The user-facing button shows a "synthesizing…"
// state via a normal HTML form submission.
func (s *server) handleSynthesize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	added, err := synthesizeProposals(ctx, s.paths, modelSonnet)
	if err != nil {
		log.Printf("synthesize: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("synthesize: %d new proposals", added)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) renderPreview(w http.ResponseWriter, r *http.Request, preview actionPreview) {
	preview.BackURL = "/"
	if ref := r.Header.Get("Referer"); strings.HasPrefix(ref, "http://"+r.Host) || strings.HasPrefix(ref, "https://"+r.Host) {
		preview.BackURL = ref
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.previewTmpl.Execute(w, preview); err != nil {
		log.Printf("preview template execute: %v", err)
	}
}

// statusRank orders observations on the page: active first, ignored at the
// bottom, promoted sandwiched in between so user can still find them.
func statusRank(s string) int {
	switch s {
	case statusActive, "":
		return 0
	case statusPromotedClaudeMD, statusPromotedToSkill:
		return 1
	case statusIgnored:
		return 2
	default:
		return 3
	}
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}
