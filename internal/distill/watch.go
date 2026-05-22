package distill

import (
	"flag"
	"fmt"
	"strings"
	"time"
)

func runWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	var (
		productName = fs.String("product", "", "sessions to process: claude | codex | all")
		interval    = fs.Duration("interval", time.Hour, "time between extraction scans")
		quietFor    = fs.Duration("quiet-for", 10*time.Minute, "ignore transcripts modified more recently than this")
		model       = fs.String("model", "haiku", "model to use: haiku | sonnet")
		maxChars    = fs.Int("max-transcript-chars", 60_000, "truncate rendered user-message excerpts longer than this")
		minTurns    = fs.Int("min-user-turns", 2, "skip sessions with fewer user turns unless high-signal language appears")
		minChars    = fs.Int("min-user-chars", 200, "skip sessions with fewer user-message chars unless high-signal language appears")
		maxObs      = fs.Int("max-observations", 80, "maximum relevant existing observations to include in extractor prompt")
		zoomChars   = fs.Int("zoom-context-chars", 2500, "max chars from the preceding assistant turn to include around high-signal user turns")
		noSkip      = fs.Bool("no-skip", false, "disable cheap local skipping for short low-signal sessions")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *interval <= 0 {
		return fmt.Errorf("interval must be positive")
	}
	if *quietFor < 0 {
		return fmt.Errorf("quiet-for must be non-negative")
	}
	targetProduct, err := parseWatchProduct(*productName)
	if err != nil {
		return err
	}
	opts := extractOpts{
		product:           targetProduct,
		recent:            0,
		onlyNew:           true,
		model:             *model,
		maxTranscriptChar: *maxChars,
		minUserTurns:      *minTurns,
		minUserChars:      *minChars,
		maxObservations:   *maxObs,
		zoomContextChars:  *zoomChars,
		noSkip:            *noSkip,
	}

	fmt.Printf("watching %s sessions every %s (quiet-for=%s)\n", targetProduct, interval.String(), quietFor.String())
	for {
		if err := runExtractOnce(opts, *quietFor); err != nil {
			fmt.Printf("watch pass failed: %v\n", err)
		}
		time.Sleep(*interval)
	}
}

func parseWatchProduct(name string) (product, error) {
	if strings.TrimSpace(name) != "" {
		return parseProduct(name)
	}
	p, err := resolvePaths()
	if err != nil {
		return "", err
	}
	prefs, err := readPreferences(p)
	if err != nil {
		return "", err
	}
	return prefs.watchProduct(), nil
}
