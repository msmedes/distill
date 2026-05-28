package distill

import (
	"flag"
	"fmt"
	"strings"
	"time"
)

func runWatch(args []string) error {
	p, err := resolvePaths()
	if err != nil {
		return err
	}
	prefs, err := readPreferences(p)
	if err != nil {
		return err
	}
	defaultInterval, err := time.ParseDuration(prefs.WatchInterval)
	if err != nil {
		return err
	}
	defaultQuietFor, err := time.ParseDuration(prefs.QuietFor)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	var (
		productName = fs.String("product", "", "sessions to process: claude | codex | all")
		interval    = fs.Duration("interval", defaultInterval, "time between extraction scans")
		quietFor    = fs.Duration("quiet-for", defaultQuietFor, "ignore transcripts modified more recently than this")
		model       = fs.String("model", prefs.ExtractionModel, "model to use")
		maxChars    = fs.Int("max-transcript-chars", prefs.MaxTranscriptChars, "truncate rendered user-message excerpts longer than this")
		minTurns    = fs.Int("min-user-turns", prefs.MinUserTurns, "skip sessions with fewer user turns unless high-signal language appears")
		minChars    = fs.Int("min-user-chars", prefs.MinUserChars, "skip sessions with fewer user-message chars unless high-signal language appears")
		maxObs      = fs.Int("max-observations", prefs.MaxObservations, "maximum relevant existing observations to include in extractor prompt")
		zoomChars   = fs.Int("zoom-context-chars", prefs.ZoomContextChars, "max chars from the preceding assistant turn to include around high-signal user turns")
		noSkip      = fs.Bool("no-skip", prefs.NoSkip, "disable cheap local skipping for short low-signal sessions")
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
			fmt.Printf("%s watch pass failed: %v\n", time.Now().Format(time.RFC3339), err)
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
