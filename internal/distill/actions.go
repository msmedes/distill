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
	CommitScope    observationScope
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

func removeProposals(proposals []proposal, artifact string, scope observationScope) []proposal {
	kept := proposals[:0]
	for _, pr := range proposals {
		normalizeProposal(&pr, scopeUser)
		if pr.Artifact == artifact && (scope == "" || pr.Scope == scope) {
			continue
		}
		kept = append(kept, pr)
	}
	if kept == nil {
		return []proposal{}
	}
	return kept
}

func previewAcceptScopedProposal(ctx context.Context, p paths, id, artifact string, scope observationScope, canCommit bool) (actionPreview, error) {
	switch artifact {
	case artifactSkill:
		return previewPromoteToSkillWithScope(ctx, p, id, scope, canCommit)
	case artifactAgentsMD:
		return previewPromoteToAgentsMDWithScope(ctx, p, id, scope, canCommit)
	}
	return actionPreview{}, fmt.Errorf("unknown proposal artifact: %s", artifact)
}

func dismissScopedProposal(p paths, id, artifact string, scope observationScope) (actionResult, error) {
	if err := newStore(p).updateObservations(func(obs []observation) ([]observation, error) {
		i, ok := findObservation(obs, id)
		if !ok {
			return nil, fmt.Errorf("observation %s not found", id)
		}
		obs[i].Proposals = removeProposals(obs[i].Proposals, artifact, scope)
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

func previewPromoteToClaudeMD(ctx context.Context, p paths, id string, canCommit bool) (actionPreview, error) {
	return previewPromoteToAgentsMDWithScope(ctx, p, id, "", canCommit)
}

func previewPromoteToAgentsMDWithScope(ctx context.Context, p paths, id string, scope observationScope, canCommit bool) (actionPreview, error) {
	prefs, err := readPreferences(p)
	if err != nil {
		return actionPreview{}, err
	}
	o, err := promotableObservation(newStore(p), id)
	if err != nil {
		return actionPreview{}, err
	}
	target, err := prefs.promotionTarget(o, artifactAgentsMD, scope)
	if err != nil {
		return actionPreview{}, err
	}
	promptObs := observationForPromotionTarget(o, target)
	body, err := readFileOrEmpty(target.Path)
	if err != nil {
		return actionPreview{}, fmt.Errorf("reading instructions (%s): %w", target.Path, err)
	}
	updated, err := generateAlwaysOnInstructions(ctx, string(body), promptObs)
	if err != nil {
		return actionPreview{}, err
	}
	preview := actionPreview{
		Title:         "preview: instructions promotion",
		Action:        "promote-claude-md",
		ObservationID: id,
		Message:       "would rewrite the scoped instruction file to integrate this observation",
		Effects: []string{
			target.Path + " would be written",
			"observations.jsonl would mark the observation as promoted-claude-md",
			"pending proposals on the observation would be cleared",
		},
		DiffLabel:   "write diff",
		Diff:        renderLineDiff(string(body), updated),
		OutputLabel: "resulting instructions file",
		Output:      updated,
	}
	if canCommit {
		preview.CanCommit = true
		preview.CommitAction = "commit-promote-claude-md"
		preview.CommitOutput = updated
		preview.CommitPath = target.Path
		preview.CommitBaseHash = sha256Hex(body)
		preview.CommitScope = target.Scope
	}
	return preview, nil
}

func previewPromoteToSkill(ctx context.Context, p paths, id string, canCommit bool) (actionPreview, error) {
	return previewPromoteToSkillWithScope(ctx, p, id, "", canCommit)
}

func previewPromoteToSkillWithScope(ctx context.Context, p paths, id string, scope observationScope, canCommit bool) (actionPreview, error) {
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
	prefs, err := readPreferences(p)
	if err != nil {
		return actionPreview{}, err
	}
	target, err := prefs.promotionTarget(o, artifactSkill, scope)
	if err != nil {
		return actionPreview{}, err
	}
	gen, err := generateSkill(ctx, observationForPromotionTarget(o, target))
	if err != nil {
		return actionPreview{}, fmt.Errorf("generating skill: %w", err)
	}
	skillPath := filepath.Join(target.Path, gen.Name, "SKILL.md")
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
		preview.CommitScope = target.Scope
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

func commitClaudeMDPreviewWithScope(p paths, id, path, output, baseHash string, scope observationScope) (actionResult, error) {
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
		target, err := prefs.promotionTarget(obs[i], artifactAgentsMD, scope)
		if err != nil {
			return nil, err
		}
		if filepath.Clean(path) != filepath.Clean(target.Path) {
			return nil, fmt.Errorf("commit path %s does not match configured instruction destination %s", path, target.Path)
		}
		current, err := readFileOrEmpty(path)
		if err != nil {
			return nil, fmt.Errorf("reading instructions (%s): %w", path, err)
		}
		if sha256Hex(current) != baseHash {
			return nil, fmt.Errorf("instructions changed since preview; regenerate the preview")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
			return nil, err
		}
		obs[i].Status = statusPromotedClaudeMD
		obs[i].PromotedTo = path
		obs[i].Proposals = []proposal{}
		result = actionResult{OK: true, Message: "added to instructions", PromotedTo: path}
		return obs, nil
	})
	return result, err
}

func commitSkillPreviewWithScope(p paths, id, skillPath, output string, scope observationScope) (actionResult, error) {
	st := newStore(p)
	prefs, err := readPreferences(p)
	if err != nil {
		return actionResult{}, err
	}
	o, err := observationByID(st, id)
	if err != nil {
		return actionResult{}, err
	}
	target, err := prefs.promotionTarget(o, artifactSkill, scope)
	if err != nil {
		return actionResult{}, err
	}
	if filepath.Base(skillPath) != "SKILL.md" || !pathWithinDir(skillPath, target.Path) {
		return actionResult{}, fmt.Errorf("skill commit path must be a SKILL.md under %s: %s", target.Path, skillPath)
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

func readFileOrEmpty(path string) ([]byte, error) {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []byte{}, nil
	}
	return body, err
}

func resolvedPromotionScope(o observation, requested observationScope) observationScope {
	if validScope(requested) {
		return requested
	}
	normalizeObservation(&o)
	return o.Scope
}

func observationForPromotionTarget(o observation, target promotionTarget) observation {
	normalizeObservation(&o)
	o.Scope = target.Scope
	if target.Scope != scopeProject {
		o.ProjectCWD = ""
	}
	return o
}

func observationScopeText(o observation) string {
	normalizeObservation(&o)
	if o.Scope == scopeProject {
		if o.ProjectCWD != "" {
			return "project (" + o.ProjectCWD + ")"
		}
		return "project"
	}
	return "user"
}

func rewriteAlwaysOnInstructions(ctx context.Context, path string, o observation) error {
	body, err := readFileOrEmpty(path)
	if err != nil {
		return fmt.Errorf("reading instructions (%s): %w", path, err)
	}
	updated, err := generateAlwaysOnInstructions(ctx, string(body), o)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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
		return "", fmt.Errorf("generating instructions: %w", err)
	}
	out := strings.TrimSpace(stripMarkdownFence(raw))
	if out == "" {
		return "", fmt.Errorf("model returned empty instructions")
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
		project := ""
		if e.ProjectCWD != "" {
			project = ", project " + e.ProjectCWD
		}
		fmt.Fprintf(&evidenceText, "- session %s%s, %s: %q\n",
			shortID(e.SessionID), project, strings.Join(e.TurnRefs, ", "), e.Quote)
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
	out = strings.ReplaceAll(out, "{{OBS_SCOPE}}", observationScopeText(o))
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
		project := ""
		if e.ProjectCWD != "" {
			project = ", project " + e.ProjectCWD
		}
		fmt.Fprintf(&evidenceText, "- session %s%s, %s: %q\n",
			shortID(e.SessionID), project, strings.Join(e.TurnRefs, ", "), e.Quote)
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
	out = strings.ReplaceAll(out, "{{OBS_SCOPE}}", observationScopeText(o))
	out = strings.ReplaceAll(out, "{{OBS_CLAIM}}", o.Claim)
	out = strings.ReplaceAll(out, "{{OBS_EVIDENCE}}", strings.TrimRight(evidenceText.String(), "\n"))
	out = strings.ReplaceAll(out, "{{OBS_NOTES}}", strings.TrimRight(notesText.String(), "\n"))
	return out, nil
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
