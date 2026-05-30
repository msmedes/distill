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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	update, err := checkForUpdates(ctx, p, &http.Client{Timeout: 2 * time.Second}, time.Now())
	cancel()
	if err != nil {
		log.Printf("update check skipped: %v", err)
	} else {
		srv.update = update
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
	update       updateStatus
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
	Update             updateStatus
}

type sessionsData struct {
	Sessions       []processedSessionView
	Count          int
	Shown          int
	Limit          int
	HasMore        bool
	Filter         string
	ProcessedCount int
	ObservedCount  int
	Update         updateStatus
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
	Evidence    int
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
		Update:      s.update,
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
	limit := parseSessionsLimit(r)
	filter := parseSessionsFilter(r)
	sessions, stats, err := s.processedSessions(limit, filter)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading sessions: %v", err), http.StatusInternalServerError)
		return
	}
	data := sessionsData{
		Sessions:       sessions,
		Count:          stats.matching,
		Shown:          len(sessions),
		Limit:          limit,
		HasMore:        stats.matching > len(sessions),
		Filter:         filter,
		ProcessedCount: stats.processed,
		ObservedCount:  stats.observed,
		Update:         s.update,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.sessionsTmpl.Execute(w, data); err != nil {
		log.Printf("sessions template execute: %v", err)
	}
}

const defaultSessionsPageLimit = 100
const maxSessionsPageLimit = 500
const sessionsFilterObservations = "observations"
const sessionsFilterAll = "all"

func parseSessionsLimit(r *http.Request) int {
	n := defaultSessionsPageLimit
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			n = parsed
		}
	}
	if n > maxSessionsPageLimit {
		return maxSessionsPageLimit
	}
	return n
}

func parseSessionsFilter(r *http.Request) string {
	switch strings.TrimSpace(r.URL.Query().Get("filter")) {
	case sessionsFilterAll:
		return sessionsFilterAll
	default:
		return sessionsFilterObservations
	}
}

type sessionsStats struct {
	matching  int
	processed int
	observed  int
}

type observedSessionInfo struct {
	Count int
	Title string
}

func (s *server) processedSessions(limit int, filter string) ([]processedSessionView, sessionsStats, error) {
	st := newStore(s.paths)
	state, err := st.readState()
	if err != nil {
		return nil, sessionsStats{}, err
	}
	observed, err := s.observedSessions()
	if err != nil {
		return nil, sessionsStats{}, err
	}
	stats := sessionsStats{
		processed: len(state.ProcessedSessions),
		observed:  len(observed),
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
		observedInfo := observed[sessionStateKey(productName, sessionID)]
		if observedInfo.Count == 0 && productName == productClaude {
			observedInfo = observed[sessionID]
		}
		if filter == sessionsFilterObservations && observedInfo.Count == 0 {
			continue
		}

		view := processedSessionView{
			Product:     productName,
			SessionID:   sessionID,
			ShortID:     shortID(sessionID),
			ProcessedAt: processedAt,
			Title:       "untitled session",
			Evidence:    observedInfo.Count,
		}
		if observedInfo.Title != "" {
			view.Title = observedInfo.Title
		}
		if hasMeta {
			view.SessionTime = meta.mtime.Format(time.RFC3339)
			view.Project = projectLabel(meta.cwd, meta.projectDir)
			view.SourcePath = meta.filePath
			view.Command = sessionResumeCommand(productName, sessionID, meta.cwd)
		}
		views = append(views, view)
	}
	if filter == sessionsFilterObservations {
		for key, observedInfo := range observed {
			if key == "" || strings.Contains(key, ":") && processedSessionKeyExists(state, key) {
				continue
			}
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
				Title:       "untitled session",
				Evidence:    observedInfo.Count,
				ProcessedAt: "not in state",
			}
			if observedInfo.Title != "" {
				view.Title = observedInfo.Title
			}
			if hasMeta {
				view.SessionTime = meta.mtime.Format(time.RFC3339)
				view.Project = projectLabel(meta.cwd, meta.projectDir)
				view.SourcePath = meta.filePath
				view.Command = sessionResumeCommand(productName, sessionID, meta.cwd)
			}
			views = append(views, view)
		}
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
	total := len(views)
	stats.matching = total
	if limit > 0 && len(views) > limit {
		views = views[:limit]
	}
	for i := range views {
		if views[i].SourcePath == "" {
			continue
		}
		if views[i].Title != "untitled session" {
			continue
		}
		title, length := summarizeSessionFile(sessionMeta{
			product:   views[i].Product,
			sessionID: views[i].SessionID,
			filePath:  views[i].SourcePath,
		})
		if title != "" {
			views[i].Title = title
		}
		views[i].Length = length
	}
	return views, stats, nil
}

func processedSessionKeyExists(state *stateFile, key string) bool {
	if _, ok := state.ProcessedSessions[key]; ok {
		return true
	}
	productName, sessionID := splitSessionStateKey(key)
	return productName == productClaude && processedSessionKeyExistsLegacy(state, sessionID)
}

func processedSessionKeyExistsLegacy(state *stateFile, sessionID string) bool {
	_, ok := state.ProcessedSessions[sessionID]
	return ok
}

func (s *server) observedSessions() (map[string]observedSessionInfo, error) {
	obs, err := newStore(s.paths).readObservations()
	if err != nil {
		return nil, err
	}
	out := map[string]observedSessionInfo{}
	for _, o := range obs {
		for _, e := range o.Evidence {
			p := e.Product
			if p == "" {
				p = productClaude
			}
			if e.SessionID == "" {
				continue
			}
			addObservedSession(out, sessionStateKey(p, e.SessionID), e.Quote)
		}
		for _, sessionID := range o.ContradictedBy {
			if sessionID == "" {
				continue
			}
			key := sessionID
			if !strings.Contains(sessionID, ":") {
				key = sessionStateKey(productClaude, sessionID)
			}
			addObservedSession(out, key, "")
		}
	}
	return out, nil
}

func addObservedSession(out map[string]observedSessionInfo, key, title string) {
	info := out[key]
	info.Count++
	if info.Title == "" && strings.TrimSpace(title) != "" {
		info.Title = compactTitle(title)
	}
	out[key] = info
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
	prefs, err := readPreferences(s.paths)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	added, err := synthesizeProposals(ctx, s.paths, prefs)
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
