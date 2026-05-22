package distill

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// storeMu serializes all observation-store mutations from the HTTP server.
// (Cross-process races with the CLI's extract command are out of scope for v1;
// in practice you don't run extract and click around at the same time.)
var storeMu sync.Mutex

const claudeMDSectionHeader = "## Auto-extracted from distill"

type actionResult struct {
	OK         bool   `json:"ok"`
	Message    string `json:"message,omitempty"`
	PromotedTo string `json:"promoted_to,omitempty"`
}

func findObservation(obs []observation, id string) (int, bool) {
	for i, o := range obs {
		if o.ID == id {
			return i, true
		}
	}
	return -1, false
}

// acceptProposal accepts the named proposal on an observation by firing the
// corresponding promote action, then clears all pending proposals on that
// observation (acceptance retires the rest implicitly).
func acceptProposal(ctx context.Context, p paths, id, kind string) (actionResult, error) {
	switch kind {
	case proposalSkill:
		res, err := promoteToSkill(ctx, p, id)
		if err != nil {
			return res, err
		}
		_ = clearProposals(p, id)
		return res, nil
	case proposalClaudeMD:
		res, err := promoteToClaudeMD(p, id)
		if err != nil {
			return res, err
		}
		_ = clearProposals(p, id)
		return res, nil
	}
	return actionResult{}, fmt.Errorf("unknown proposal kind: %s", kind)
}

func dismissProposal(p paths, id, kind string) (actionResult, error) {
	storeMu.Lock()
	defer storeMu.Unlock()

	obs, err := readObservations(p.observationFile)
	if err != nil {
		return actionResult{}, err
	}
	i, ok := findObservation(obs, id)
	if !ok {
		return actionResult{}, fmt.Errorf("observation %s not found", id)
	}
	kept := obs[i].Proposals[:0]
	for _, pr := range obs[i].Proposals {
		if pr.Kind == kind {
			continue
		}
		kept = append(kept, pr)
	}
	obs[i].Proposals = kept
	if err := writeObservations(p.observationFile, obs); err != nil {
		return actionResult{}, err
	}
	return actionResult{OK: true, Message: "dismissed"}, nil
}

func clearProposals(p paths, id string) error {
	// promote* released storeMu on return; we re-acquire here for the small
	// follow-up write. Two-step is non-atomic — the UI may briefly show
	// status=promoted alongside a stale proposal — acceptable for v1.
	storeMu.Lock()
	defer storeMu.Unlock()
	obs, err := readObservations(p.observationFile)
	if err != nil {
		return err
	}
	i, ok := findObservation(obs, id)
	if !ok {
		return nil
	}
	obs[i].Proposals = []proposal{}
	return writeObservations(p.observationFile, obs)
}

func setStatus(p paths, id, status string) (actionResult, error) {
	storeMu.Lock()
	defer storeMu.Unlock()

	obs, err := readObservations(p.observationFile)
	if err != nil {
		return actionResult{}, err
	}
	i, ok := findObservation(obs, id)
	if !ok {
		return actionResult{}, fmt.Errorf("observation %s not found", id)
	}
	obs[i].Status = status
	if status == statusActive {
		obs[i].PromotedTo = ""
	}
	if err := writeObservations(p.observationFile, obs); err != nil {
		return actionResult{}, err
	}
	return actionResult{OK: true, Message: status}, nil
}

func addNote(p paths, id, text string) (actionResult, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return actionResult{}, fmt.Errorf("empty note")
	}

	storeMu.Lock()
	defer storeMu.Unlock()

	obs, err := readObservations(p.observationFile)
	if err != nil {
		return actionResult{}, err
	}
	i, ok := findObservation(obs, id)
	if !ok {
		return actionResult{}, fmt.Errorf("observation %s not found", id)
	}
	obs[i].Notes = append(obs[i].Notes, note{
		At:   time.Now().UTC().Format(time.RFC3339),
		Text: text,
	})
	if err := writeObservations(p.observationFile, obs); err != nil {
		return actionResult{}, err
	}
	return actionResult{OK: true, Message: "noted"}, nil
}

func promoteToClaudeMD(p paths, id string) (actionResult, error) {
	storeMu.Lock()
	defer storeMu.Unlock()

	obs, err := readObservations(p.observationFile)
	if err != nil {
		return actionResult{}, err
	}
	i, ok := findObservation(obs, id)
	if !ok {
		return actionResult{}, fmt.Errorf("observation %s not found", id)
	}
	o := obs[i]

	claudePath, err := claudeMDPath()
	if err != nil {
		return actionResult{}, err
	}

	if err := appendToClaudeMD(claudePath, o); err != nil {
		return actionResult{}, err
	}

	obs[i].Status = statusPromotedClaudeMD
	obs[i].PromotedTo = claudePath
	if err := writeObservations(p.observationFile, obs); err != nil {
		return actionResult{}, err
	}
	return actionResult{OK: true, Message: "added to CLAUDE.md", PromotedTo: claudePath}, nil
}

func promoteToSkill(ctx context.Context, p paths, id string) (actionResult, error) {
	storeMu.Lock()
	defer storeMu.Unlock()

	obs, err := readObservations(p.observationFile)
	if err != nil {
		return actionResult{}, err
	}
	i, ok := findObservation(obs, id)
	if !ok {
		return actionResult{}, fmt.Errorf("observation %s not found", id)
	}
	o := obs[i]

	gen, err := generateSkill(ctx, o)
	if err != nil {
		return actionResult{}, fmt.Errorf("generating skill: %w", err)
	}

	skillPath, err := writeSkillFile(gen)
	if err != nil {
		return actionResult{}, fmt.Errorf("writing skill: %w", err)
	}

	obs[i].Status = statusPromotedToSkill
	obs[i].PromotedTo = skillPath
	if err := writeObservations(p.observationFile, obs); err != nil {
		return actionResult{}, err
	}
	return actionResult{OK: true, Message: "skill extracted", PromotedTo: skillPath}, nil
}

func claudeMDPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "CLAUDE.md"), nil
}

func appendToClaudeMD(path string, o observation) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading CLAUDE.md (%s): %w", path, err)
	}
	src := string(body)
	header := claudeMDSectionHeader
	now := time.Now().UTC().Format("2006-01-02")
	line := fmt.Sprintf("- %s _(distill %s, %s)_", o.Claim, o.ID, now)

	var updated string
	if strings.Contains(src, header) {
		updated = appendUnderHeader(src, header, line)
	} else {
		sep := "\n\n"
		if strings.HasSuffix(src, "\n") {
			sep = "\n"
		}
		updated = src + sep + header + "\n\n" + line + "\n"
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}

// appendUnderHeader inserts a bullet line at the end of an existing markdown
// section identified by its header line, before the next header or EOF.
func appendUnderHeader(src, header, line string) string {
	idx := strings.Index(src, header)
	if idx < 0 {
		return src + "\n\n" + header + "\n\n" + line + "\n"
	}
	// Find the start of the next top-or-same-level header after our section,
	// or end-of-file.
	rest := src[idx+len(header):]
	nextHeader := findNextHeader(rest, header)
	insertAt := idx + len(header) + nextHeader
	before := strings.TrimRight(src[:insertAt], " \n")
	after := src[insertAt:]
	return before + "\n" + line + "\n\n" + strings.TrimLeft(after, "\n")
}

func findNextHeader(rest, currentHeader string) int {
	// Match lines starting with # at the same level or higher (fewer #).
	level := len(currentHeader) - len(strings.TrimLeft(currentHeader, "#"))
	if level == 0 {
		level = 2
	}
	lines := strings.SplitAfter(rest, "\n")
	pos := 0
	for _, l := range lines {
		trimmed := strings.TrimLeft(l, " ")
		if strings.HasPrefix(trimmed, "#") {
			h := strings.TrimLeft(trimmed, "#")
			thisLevel := len(trimmed) - len(h)
			if thisLevel > 0 && thisLevel <= level && pos > 0 {
				return pos
			}
		}
		pos += len(l)
	}
	return len(rest)
}

type generatedSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

func generateSkill(ctx context.Context, o observation) (generatedSkill, error) {
	prompt, err := buildSkillPrompt(o)
	if err != nil {
		return generatedSkill{}, err
	}
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
	}
	raw, err := callClaude(ctx, modelSonnet, prompt)
	if err != nil {
		return generatedSkill{}, err
	}
	jsonText := extractJSONBlock(raw)
	var gen generatedSkill
	if err := json.Unmarshal([]byte(jsonText), &gen); err != nil {
		return generatedSkill{}, fmt.Errorf("parsing skill JSON: %w\nraw: %s", err, truncate(raw, 1000))
	}
	gen.Name = slugify(gen.Name)
	if gen.Name == "" {
		return generatedSkill{}, fmt.Errorf("model returned empty skill name")
	}
	if gen.Description == "" || gen.Body == "" {
		return generatedSkill{}, fmt.Errorf("model returned incomplete skill (missing description or body)")
	}
	return gen, nil
}

func buildSkillPrompt(o observation) (string, error) {
	raw, err := promptsFS.ReadFile("prompts/promote-skill.md")
	if err != nil {
		return "", err
	}
	var evidenceText strings.Builder
	for _, e := range o.Evidence {
		fmt.Fprintf(&evidenceText, "- session %s, %s: %q\n",
			shortID(e.SessionID), strings.Join(e.TurnRefs, ", "), e.Quote)
	}
	var notesText strings.Builder
	for _, n := range o.Notes {
		fmt.Fprintf(&notesText, "- %s\n", n.Text)
	}
	if notesText.Len() == 0 {
		notesText.WriteString("(none)")
	}
	out := string(raw)
	out = strings.ReplaceAll(out, "{{OBS_ID}}", o.ID)
	out = strings.ReplaceAll(out, "{{OBS_TYPE}}", string(o.Type))
	out = strings.ReplaceAll(out, "{{OBS_CLAIM}}", o.Claim)
	out = strings.ReplaceAll(out, "{{OBS_EVIDENCE}}", strings.TrimRight(evidenceText.String(), "\n"))
	out = strings.ReplaceAll(out, "{{OBS_NOTES}}", strings.TrimRight(notesText.String(), "\n"))
	return out, nil
}

func writeSkillFile(g generatedSkill) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".claude", "skills", g.Name)
	skillPath := filepath.Join(dir, "SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		return "", fmt.Errorf("skill %q already exists at %s — rename or delete first", g.Name, skillPath)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n",
		g.Name, escapeYAMLString(g.Description), strings.TrimSpace(g.Body))
	if err := os.WriteFile(skillPath, []byte(body), 0o644); err != nil {
		return "", err
	}
	return skillPath, nil
}

func escapeYAMLString(s string) string {
	// Description is a single line in YAML. If it contains characters that
	// would confuse the parser, wrap in single quotes and escape any.
	if strings.ContainsAny(s, "':\n#") {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return s
}

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	s = slugRe.ReplaceAllString(s, "")
	s = strings.Trim(s, "-")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
