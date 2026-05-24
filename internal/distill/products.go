package distill

import "fmt"

type product string

const (
	productClaude product = "claude"
	productCodex  product = "codex"
	productAll    product = "all"
)

func parseProduct(s string) (product, error) {
	switch product(s) {
	case "", productAll:
		return productAll, nil
	case productClaude:
		return productClaude, nil
	case productCodex:
		return productCodex, nil
	default:
		return "", fmt.Errorf("unknown product: %s", s)
	}
}

type productSource struct {
	product         product
	listSessions    func(paths) ([]sessionMeta, error)
	parseTranscript func(string) ([]transcriptTurn, error)
}

func productSources(target product) []productSource {
	sources := []productSource{
		{
			product:         productClaude,
			listSessions:    func(p paths) ([]sessionMeta, error) { return listClaudeSessions(p.claudeProjects) },
			parseTranscript: parseTranscript,
		},
		{
			product:         productCodex,
			listSessions:    func(p paths) ([]sessionMeta, error) { return listCodexSessions(p.codexSessions) },
			parseTranscript: parseCodexTranscript,
		},
	}
	if target == productAll {
		return sources
	}
	var selected []productSource
	for _, source := range sources {
		if source.product == target {
			selected = append(selected, source)
		}
	}
	return selected
}

func productSourceFor(p product) (productSource, bool) {
	for _, source := range productSources(productAll) {
		if source.product == p {
			return source, true
		}
	}
	return productSource{}, false
}

func sessionStateKey(p product, sessionID string) string {
	return string(p) + ":" + sessionID
}

func processedSession(state *stateFile, s sessionMeta) bool {
	if _, seen := state.ProcessedSessions[sessionStateKey(s.product, s.sessionID)]; seen {
		return true
	}
	// Backward compatibility for state written before sessions had products.
	if s.product == productClaude {
		_, seen := state.ProcessedSessions[s.sessionID]
		return seen
	}
	return false
}
