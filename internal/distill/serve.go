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
	paths       paths
	indexTmpl   *template.Template
	helpTmpl    *template.Template
	previewTmpl *template.Template
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

const (
	webRunBatchLimit = 5
	webRunQuietFor   = 10 * time.Minute
)

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
		RunBatchLimit:      webRunBatchLimit,
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

func (s *server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	err := writePreferences(s.paths, preferences{
		AlwaysOnPath: r.FormValue("always_on_path"),
		SkillsDir:    r.FormValue("skills_dir"),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
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
		recent:            webRunBatchLimit,
		onlyNew:           true,
		model:             "haiku",
		maxTranscriptChar: 60_000,
		minUserTurns:      2,
		minUserChars:      200,
		maxObservations:   80,
		zoomContextChars:  2500,
	}
	if err := runExtractWithPaths(s.paths, opts, webRunQuietFor); err != nil {
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
