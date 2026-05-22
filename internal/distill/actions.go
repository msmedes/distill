package distill

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

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

func removeProposalsOfKind(proposals []proposal, kind string) []proposal {
	kept := proposals[:0]
	for _, pr := range proposals {
		if pr.Kind == kind {
			continue
		}
		kept = append(kept, pr)
	}
	if kept == nil {
		return []proposal{}
	}
	return kept
}

// acceptProposal accepts the named proposal on an observation by firing the
// corresponding promote action, then clears all pending proposals on that
// observation (acceptance retires the rest implicitly).
func acceptProposal(ctx context.Context, p paths, id, kind string) (actionResult, error) {
	switch kind {
	case proposalSkill:
		return promoteToSkill(ctx, p, id)
	case proposalClaudeMD:
		return promoteToClaudeMD(p, id)
	}
	return actionResult{}, fmt.Errorf("unknown proposal kind: %s", kind)
}

func dismissProposal(p paths, id, kind string) (actionResult, error) {
	if err := newStore(p).updateObservations(func(obs []observation) ([]observation, error) {
		i, ok := findObservation(obs, id)
		if !ok {
			return nil, fmt.Errorf("observation %s not found", id)
		}
		obs[i].Proposals = removeProposalsOfKind(obs[i].Proposals, kind)
		return obs, nil
	}); err != nil {
		return actionResult{}, err
	}
	return actionResult{OK: true, Message: "dismissed"}, nil
}

func setStatus(p paths, id, status string) (actionResult, error) {
	if err := newStore(p).updateObservations(func(obs []observation) ([]observation, error) {
		i, ok := findObservation(obs, id)
		if !ok {
			return nil, fmt.Errorf("observation %s not found", id)
		}
		obs[i].Status = status
		if status == statusActive {
			obs[i].PromotedTo = ""
		}
		return obs, nil
	}); err != nil {
		return actionResult{}, err
	}
	return actionResult{OK: true, Message: status}, nil
}

func addNote(p paths, id, text string) (actionResult, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return actionResult{}, fmt.Errorf("empty note")
	}

	if err := newStore(p).updateObservations(func(obs []observation) ([]observation, error) {
		i, ok := findObservation(obs, id)
		if !ok {
			return nil, fmt.Errorf("observation %s not found", id)
		}
		obs[i].Notes = append(obs[i].Notes, note{
			At:   time.Now().UTC().Format(time.RFC3339),
			Text: text,
		})
		return obs, nil
	}); err != nil {
		return actionResult{}, err
	}
	return actionResult{OK: true, Message: "noted"}, nil
}

func promoteToClaudeMD(p paths, id string) (actionResult, error) {
	prefs, err := readPreferences(p)
	if err != nil {
		return actionResult{}, err
	}
	alwaysOnPath := prefs.alwaysOnDestination(observation{})

	if err := newStore(p).updateObservations(func(obs []observation) ([]observation, error) {
		i, ok := findObservation(obs, id)
		if !ok {
			return nil, fmt.Errorf("observation %s not found", id)
		}
		alwaysOnPath = prefs.alwaysOnDestination(obs[i])
		if obs[i].Status == statusPromotedClaudeMD && obs[i].PromotedTo == alwaysOnPath {
			obs[i].Proposals = []proposal{}
			return obs, nil
		}
		if err := appendToClaudeMD(alwaysOnPath, obs[i]); err != nil {
			return nil, err
		}
		obs[i].Status = statusPromotedClaudeMD
		obs[i].PromotedTo = alwaysOnPath
		obs[i].Proposals = []proposal{}
		return obs, nil
	}); err != nil {
		return actionResult{}, err
	}
	return actionResult{OK: true, Message: "added to always-on instructions", PromotedTo: alwaysOnPath}, nil
}

func promoteToSkill(ctx context.Context, p paths, id string) (actionResult, error) {
	st := newStore(p)
	var result actionResult
	err := st.withNamedLock("promote-skill-"+id, func() error {
		o, err := promotableObservation(st, id)
		if err != nil {
			return err
		}
		if o.Status == statusPromotedToSkill && o.PromotedTo != "" {
			result = actionResult{OK: true, Message: "skill extracted", PromotedTo: o.PromotedTo}
			return nil
		}

		gen, err := generateSkill(ctx, o)
		if err != nil {
			return fmt.Errorf("generating skill: %w", err)
		}

		prefs, err := readPreferences(p)
		if err != nil {
			return err
		}
		skillPath, err := writeSkillFile(prefs.SkillsDir, gen)
		if err != nil {
			return fmt.Errorf("writing skill: %w", err)
		}

		if err := finalizeSkillPromotion(st, id, skillPath); err != nil {
			_ = os.Remove(skillPath)
			return err
		}
		result = actionResult{OK: true, Message: "skill extracted", PromotedTo: skillPath}
		return nil
	})
	return result, err
}

func promotableObservation(st store, id string) (observation, error) {
	obs, err := st.readObservations()
	if err != nil {
		return observation{}, err
	}
	i, ok := findObservation(obs, id)
	if !ok {
		return observation{}, fmt.Errorf("observation %s not found", id)
	}
	if obs[i].Status == statusPromotedToSkill && obs[i].PromotedTo != "" {
		return obs[i], nil
	}
	if obs[i].Status != statusActive {
		return observation{}, fmt.Errorf("observation %s is no longer active", id)
	}
	return obs[i], nil
}

func finalizeSkillPromotion(st store, id, skillPath string) error {
	return st.updateObservations(func(obs []observation) ([]observation, error) {
		i, ok := findObservation(obs, id)
		if !ok {
			return nil, fmt.Errorf("observation %s not found", id)
		}
		if obs[i].Status != statusActive {
			return nil, fmt.Errorf("observation %s is no longer active", id)
		}
		obs[i].Status = statusPromotedToSkill
		obs[i].PromotedTo = skillPath
		obs[i].Proposals = []proposal{}
		return obs, nil
	})
}

func appendToClaudeMD(path string, o observation) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading always-on instructions (%s): %w", path, err)
	}
	src := string(body)
	header := claudeMDSectionHeader
	now := time.Now().UTC().Format("2006-01-02")
	line := fmt.Sprintf("- %s _(distill %s, %s)_", o.Claim, o.ID, now)
	if strings.Contains(src, fmt.Sprintf("_(distill %s,", o.ID)) {
		return nil
	}

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

func writeSkillFile(skillsDir string, g generatedSkill) (string, error) {
	dir := filepath.Join(skillsDir, g.Name)
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
