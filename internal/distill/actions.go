package distill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type actionResult struct {
	OK         bool   `json:"ok"`
	Message    string `json:"message,omitempty"`
	PromotedTo string `json:"promoted_to,omitempty"`
}

type actionPreview struct {
	Title          string
	Action         string
	ObservationID  string
	Message        string
	Effects        []string
	DiffLabel      string
	Diff           []diffLine
	OutputLabel    string
	Output         string
	BackURL        string
	CanCommit      bool
	CommitAction   string
	CommitKind     string
	CommitOutput   string
	CommitPath     string
	CommitBaseHash string
}

type diffLine struct {
	Kind string
	Text string
}

var generateAlwaysOnInstructions = generateAlwaysOnInstructionsWithClaude

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

func previewAcceptProposal(ctx context.Context, p paths, id, kind string, canCommit bool) (actionPreview, error) {
	switch kind {
	case proposalSkill:
		return previewPromoteToSkill(ctx, p, id, canCommit)
	case proposalClaudeMD:
		return previewPromoteToClaudeMD(ctx, p, id, canCommit)
	}
	return actionPreview{}, fmt.Errorf("unknown proposal kind: %s", kind)
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
		if err := rewriteAlwaysOnInstructions(nil, alwaysOnPath, obs[i]); err != nil {
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

func previewPromoteToClaudeMD(ctx context.Context, p paths, id string, canCommit bool) (actionPreview, error) {
	prefs, err := readPreferences(p)
	if err != nil {
		return actionPreview{}, err
	}
	o, err := promotableObservation(newStore(p), id)
	if err != nil {
		return actionPreview{}, err
	}
	alwaysOnPath := prefs.alwaysOnDestination(o)
	body, err := os.ReadFile(alwaysOnPath)
	if err != nil {
		return actionPreview{}, fmt.Errorf("reading always-on instructions (%s): %w", alwaysOnPath, err)
	}
	updated, err := generateAlwaysOnInstructions(ctx, string(body), o)
	if err != nil {
		return actionPreview{}, err
	}
	preview := actionPreview{
		Title:         "preview: always-on promotion",
		Action:        "promote-claude-md",
		ObservationID: id,
		Message:       "would rewrite the always-on instruction file to integrate this observation",
		Effects: []string{
			alwaysOnPath + " would be written",
			"observations.jsonl would mark the observation as promoted-claude-md",
			"pending proposals on the observation would be cleared",
		},
		DiffLabel:   "write diff",
		Diff:        renderLineDiff(string(body), updated),
		OutputLabel: "resulting always-on file",
		Output:      updated,
	}
	if canCommit {
		preview.CanCommit = true
		preview.CommitAction = "commit-promote-claude-md"
		preview.CommitOutput = updated
		preview.CommitPath = alwaysOnPath
		preview.CommitBaseHash = sha256Hex(body)
	}
	return preview, nil
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

func previewPromoteToSkill(ctx context.Context, p paths, id string, canCommit bool) (actionPreview, error) {
	o, err := promotableObservation(newStore(p), id)
	if err != nil {
		return actionPreview{}, err
	}
	if o.Status == statusPromotedToSkill && o.PromotedTo != "" {
		return actionPreview{
			Title:         "preview: skill promotion",
			Action:        "promote-skill",
			ObservationID: id,
			Message:       "would leave the existing skill unchanged because this observation is already promoted",
			Effects:       []string{"no files would be written"},
			OutputLabel:   "existing skill path",
			Output:        o.PromotedTo,
		}, nil
	}
	gen, err := generateSkill(ctx, o)
	if err != nil {
		return actionPreview{}, fmt.Errorf("generating skill: %w", err)
	}
	prefs, err := readPreferences(p)
	if err != nil {
		return actionPreview{}, err
	}
	skillPath := filepath.Join(prefs.SkillsDir, gen.Name, "SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		return actionPreview{}, fmt.Errorf("skill %q already exists at %s — rename or delete first", gen.Name, skillPath)
	} else if !os.IsNotExist(err) {
		return actionPreview{}, err
	}
	output := renderSkillMarkdown(gen)
	preview := actionPreview{
		Title:         "preview: skill promotion",
		Action:        "promote-skill",
		ObservationID: id,
		Message:       "would generate and write a new SKILL.md",
		Effects: []string{
			skillPath + " would be written",
			"observations.jsonl would mark the observation as promoted-skill",
			"pending proposals on the observation would be cleared",
		},
		DiffLabel:   "new file",
		Diff:        renderLineDiff("", output),
		OutputLabel: "generated SKILL.md",
		Output:      output,
	}
	if canCommit {
		preview.CanCommit = true
		preview.CommitAction = "commit-promote-skill"
		preview.CommitOutput = output
		preview.CommitPath = skillPath
	}
	return preview, nil
}

func promotableObservation(st store, id string) (observation, error) {
	o, err := observationByID(st, id)
	if err != nil {
		return observation{}, err
	}
	if o.Status == statusPromotedToSkill && o.PromotedTo != "" {
		return o, nil
	}
	if o.Status != statusActive {
		return observation{}, fmt.Errorf("observation %s is no longer active", id)
	}
	return o, nil
}

func observationByID(st store, id string) (observation, error) {
	obs, err := st.readObservations()
	if err != nil {
		return observation{}, err
	}
	i, ok := findObservation(obs, id)
	if !ok {
		return observation{}, fmt.Errorf("observation %s not found", id)
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

func commitClaudeMDPreview(p paths, id, path, output, baseHash string) (actionResult, error) {
	st := newStore(p)
	prefs, err := readPreferences(p)
	if err != nil {
		return actionResult{}, err
	}
	var result actionResult
	err = st.updateObservations(func(obs []observation) ([]observation, error) {
		i, ok := findObservation(obs, id)
		if !ok {
			return nil, fmt.Errorf("observation %s not found", id)
		}
		if obs[i].Status != statusActive {
			return nil, fmt.Errorf("observation %s is no longer active", id)
		}
		expectedPath := prefs.alwaysOnDestination(obs[i])
		if filepath.Clean(path) != filepath.Clean(expectedPath) {
			return nil, fmt.Errorf("commit path %s does not match configured always-on destination %s", path, expectedPath)
		}
		current, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading always-on instructions (%s): %w", path, err)
		}
		if sha256Hex(current) != baseHash {
			return nil, fmt.Errorf("always-on instructions changed since preview; regenerate the preview")
		}
		if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
			return nil, err
		}
		obs[i].Status = statusPromotedClaudeMD
		obs[i].PromotedTo = path
		obs[i].Proposals = []proposal{}
		result = actionResult{OK: true, Message: "added to always-on instructions", PromotedTo: path}
		return obs, nil
	})
	return result, err
}

func commitSkillPreview(p paths, id, skillPath, output string) (actionResult, error) {
	st := newStore(p)
	prefs, err := readPreferences(p)
	if err != nil {
		return actionResult{}, err
	}
	if filepath.Base(skillPath) != "SKILL.md" || !pathWithinDir(skillPath, prefs.SkillsDir) {
		return actionResult{}, fmt.Errorf("skill commit path must be a SKILL.md under %s: %s", prefs.SkillsDir, skillPath)
	}
	if _, err := os.Stat(skillPath); err == nil {
		return actionResult{}, fmt.Errorf("skill already exists at %s — rename or delete first", skillPath)
	} else if !os.IsNotExist(err) {
		return actionResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		return actionResult{}, err
	}
	if err := os.WriteFile(skillPath, []byte(output), 0o644); err != nil {
		return actionResult{}, err
	}
	if err := finalizeSkillPromotion(st, id, skillPath); err != nil {
		_ = os.Remove(skillPath)
		return actionResult{}, err
	}
	return actionResult{OK: true, Message: "skill extracted", PromotedTo: skillPath}, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func renderLineDiff(before, after string) []diffLine {
	beforeLines := splitDiffLines(before)
	afterLines := splitDiffLines(after)
	start := 0
	for start < len(beforeLines) && start < len(afterLines) && beforeLines[start] == afterLines[start] {
		start++
	}
	endBefore := len(beforeLines)
	endAfter := len(afterLines)
	for endBefore > start && endAfter > start && beforeLines[endBefore-1] == afterLines[endAfter-1] {
		endBefore--
		endAfter--
	}

	var diff []diffLine
	if start > 0 {
		diff = append(diff, diffLine{Kind: "context", Text: contextMarker(start, "unchanged before")})
	}
	for _, line := range beforeLines[start:endBefore] {
		diff = append(diff, diffLine{Kind: "removed", Text: line})
	}
	for _, line := range afterLines[start:endAfter] {
		diff = append(diff, diffLine{Kind: "added", Text: line})
	}
	if len(beforeLines)-endBefore > 0 || len(afterLines)-endAfter > 0 {
		unchanged := len(beforeLines) - endBefore
		if len(afterLines)-endAfter > unchanged {
			unchanged = len(afterLines) - endAfter
		}
		diff = append(diff, diffLine{Kind: "context", Text: contextMarker(unchanged, "unchanged after")})
	}
	if len(diff) == 0 {
		return []diffLine{{Kind: "context", Text: "no content changes"}}
	}
	return diff
}

func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\n")
	}
	return lines
}

func contextMarker(count int, label string) string {
	if count == 1 {
		return fmt.Sprintf("... 1 line %s ...", label)
	}
	return fmt.Sprintf("... %d lines %s ...", count, label)
}

func pathWithinDir(path, dir string) bool {
	cleanPath := filepath.Clean(path)
	cleanDir := filepath.Clean(dir)
	rel, err := filepath.Rel(cleanDir, cleanPath)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".."
}

func rewriteAlwaysOnInstructions(ctx context.Context, path string, o observation) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading always-on instructions (%s): %w", path, err)
	}
	updated, err := generateAlwaysOnInstructions(ctx, string(body), o)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}

func generateAlwaysOnInstructionsWithClaude(ctx context.Context, current string, o observation) (string, error) {
	prompt, err := buildAlwaysOnPrompt(current, o)
	if err != nil {
		return "", err
	}
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
	}
	raw, err := callClaude(ctx, modelOpus, prompt)
	if err != nil {
		return "", fmt.Errorf("generating always-on instructions: %w", err)
	}
	out := strings.TrimSpace(stripMarkdownFence(raw))
	if out == "" {
		return "", fmt.Errorf("model returned empty always-on instructions")
	}
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out, nil
}

func buildAlwaysOnPrompt(current string, o observation) (string, error) {
	raw, err := promptsFS.ReadFile("prompts/promote-always-on.md")
	if err != nil {
		return "", err
	}
	var evidenceText strings.Builder
	for _, e := range o.Evidence {
		fmt.Fprintf(&evidenceText, "- session %s, %s: %q\n",
			shortID(e.SessionID), strings.Join(e.TurnRefs, ", "), e.Quote)
	}
	if evidenceText.Len() == 0 {
		evidenceText.WriteString("(none)")
	}
	var notesText strings.Builder
	for _, n := range o.Notes {
		fmt.Fprintf(&notesText, "- %s\n", n.Text)
	}
	if notesText.Len() == 0 {
		notesText.WriteString("(none)")
	}
	out := string(raw)
	out = strings.ReplaceAll(out, "{{CURRENT_ALWAYS_ON}}", strings.TrimRight(current, "\n"))
	out = strings.ReplaceAll(out, "{{OBS_ID}}", o.ID)
	out = strings.ReplaceAll(out, "{{OBS_TYPE}}", string(o.Type))
	out = strings.ReplaceAll(out, "{{OBS_CLAIM}}", o.Claim)
	out = strings.ReplaceAll(out, "{{OBS_EVIDENCE}}", strings.TrimRight(evidenceText.String(), "\n"))
	out = strings.ReplaceAll(out, "{{OBS_NOTES}}", strings.TrimRight(notesText.String(), "\n"))
	return out, nil
}

func stripMarkdownFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	rest := strings.TrimPrefix(s, "```")
	if i := strings.Index(rest, "\n"); i >= 0 {
		rest = rest[i+1:]
	}
	if end := strings.LastIndex(rest, "```"); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
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
	if err := os.WriteFile(skillPath, []byte(renderSkillMarkdown(g)), 0o644); err != nil {
		return "", err
	}
	return skillPath, nil
}

func renderSkillMarkdown(g generatedSkill) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n",
		g.Name, escapeYAMLString(g.Description), strings.TrimSpace(g.Body))
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
