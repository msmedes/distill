package distill

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

type sessionIndex struct {
	db *sql.DB
}

const sessionIndexWorkers = 8

type sessionIndexStats struct {
	total     int
	indexed   int64
	inserted  int64
	reused    int64
	deleted   int64
	startedAt time.Time
}

type indexedSession struct {
	product   product
	sessionID string
	filePath  string
	mtime     time.Time
	cwd       string
}

func openSessionIndex(p paths) (*sessionIndex, error) {
	if err := os.MkdirAll(filepath.Dir(p.sessionIndexFile), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", p.sessionIndexFile)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	idx := &sessionIndex{db: db}
	if err := idx.ensureSchema(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return idx, nil
}

func (i *sessionIndex) close() error {
	if i == nil || i.db == nil {
		return nil
	}
	return i.db.Close()
}

func (i *sessionIndex) ensureSchema(ctx context.Context) error {
	_, err := i.db.ExecContext(ctx, `
		PRAGMA journal_mode = WAL;
		PRAGMA busy_timeout = 250;

		CREATE TABLE IF NOT EXISTS sessions (
			product TEXT NOT NULL,
			session_id TEXT NOT NULL,
			path TEXT PRIMARY KEY,
			mtime_ns INTEGER NOT NULL,
			size INTEGER NOT NULL,
			cwd TEXT NOT NULL DEFAULT '',
			indexed_at_ns INTEGER NOT NULL,
			deleted_at_ns INTEGER,
			last_index_error TEXT NOT NULL DEFAULT ''
		);

		CREATE INDEX IF NOT EXISTS sessions_product_id_idx ON sessions(product, session_id);
		CREATE INDEX IF NOT EXISTS sessions_ready_idx ON sessions(deleted_at_ns, mtime_ns DESC);
		CREATE INDEX IF NOT EXISTS sessions_product_ready_idx ON sessions(product, deleted_at_ns, mtime_ns DESC);

		CREATE TABLE IF NOT EXISTS session_index_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
	`)
	return err
}

func refreshSessionIndex(ctx context.Context, p paths, target product) error {
	idx, err := openSessionIndex(p)
	if err != nil {
		return err
	}
	defer idx.close()

	if err := idx.setMeta(ctx, "refreshing", "1"); err != nil {
		return err
	}
	defer func() {
		_ = idx.setMeta(context.Background(), "refreshing", "0")
	}()

	started := time.Now()
	stats := &sessionIndexStats{startedAt: started}
	if err := idx.setMeta(ctx, "refresh_started_at_ns", strconv.FormatInt(started.UnixNano(), 10)); err != nil {
		return err
	}

	candidates, err := listSessionCandidates(p, target)
	if err != nil {
		return err
	}
	stats.total = len(candidates)
	log.Printf("session index: indexing %d %s session file(s)", stats.total, target)
	seen := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		seen[c.filePath] = true
	}
	if err := idx.upsertCandidates(ctx, candidates, stats); err != nil {
		return err
	}
	deleted, err := idx.markDeletedExcept(ctx, target, seen, time.Now())
	if err != nil {
		return err
	}
	atomic.StoreInt64(&stats.deleted, int64(deleted))
	log.Printf("session index: indexed %d file(s) in %s (%d new/changed, %d unchanged, %d deleted)",
		stats.total,
		time.Since(started).Round(time.Millisecond),
		atomic.LoadInt64(&stats.inserted),
		atomic.LoadInt64(&stats.reused),
		deleted,
	)
	return idx.setMeta(ctx, "refresh_finished_at_ns", strconv.FormatInt(time.Now().UnixNano(), 10))
}

func startSessionIndexRefresh(p paths, target product) {
	go func() {
		if err := refreshSessionIndex(context.Background(), p, target); err != nil {
			log.Printf("session index refresh: %v", err)
		}
	}()
}

func (i *sessionIndex) upsertCandidates(ctx context.Context, candidates []sessionCandidate, stats *sessionIndexStats) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan sessionCandidate)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	worker := func() {
		defer wg.Done()
		for c := range jobs {
			if err := ctx.Err(); err != nil {
				return
			}
			reused, err := i.upsertCandidate(ctx, c)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
				return
			}
			done := atomic.AddInt64(&stats.indexed, 1)
			if reused {
				atomic.AddInt64(&stats.reused, 1)
			} else {
				atomic.AddInt64(&stats.inserted, 1)
			}
			if done == int64(stats.total) || done%500 == 0 {
				log.Printf("session index: %d/%d files indexed", done, stats.total)
			}
		}
	}

	workers := sessionIndexWorkers
	if len(candidates) < workers {
		workers = len(candidates)
	}
	for range workers {
		wg.Add(1)
		go worker()
	}
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			break
		}
		jobs <- c
	}
	close(jobs)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	return firstErr
}

func (i *sessionIndex) upsertCandidate(ctx context.Context, c sessionCandidate) (bool, error) {
	info, err := os.Stat(c.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}

	var existingProduct string
	var existingMTime, existingSize int64
	err = i.db.QueryRowContext(ctx, `
		SELECT product, mtime_ns, size
		FROM sessions
		WHERE path = ?
	`, c.filePath).Scan(&existingProduct, &existingMTime, &existingSize)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	mtimeNS := info.ModTime().UnixNano()
	if err == nil && existingProduct == string(c.product) && existingMTime == mtimeNS && existingSize == info.Size() {
		_, err := i.db.ExecContext(ctx, `
			UPDATE sessions
			SET deleted_at_ns = NULL, indexed_at_ns = ?
			WHERE path = ?
		`, time.Now().UnixNano(), c.filePath)
		return true, err
	}

	s := c.hydrate()
	_, err = i.db.ExecContext(ctx, `
		INSERT INTO sessions (
			product, session_id, path, mtime_ns, size, cwd, indexed_at_ns, deleted_at_ns, last_index_error
		) VALUES (?, ?, ?, ?, ?, ?, ?, NULL, '')
		ON CONFLICT(path) DO UPDATE SET
			product = excluded.product,
			session_id = excluded.session_id,
			path = excluded.path,
			mtime_ns = excluded.mtime_ns,
			size = excluded.size,
			cwd = excluded.cwd,
			indexed_at_ns = excluded.indexed_at_ns,
			deleted_at_ns = NULL,
			last_index_error = ''
	`, string(s.product), s.sessionID, s.filePath, s.mtime.UnixNano(), info.Size(), s.cwd, time.Now().UnixNano())
	return false, err
}

func (i *sessionIndex) markDeletedExcept(ctx context.Context, target product, seen map[string]bool, deletedAt time.Time) (int, error) {
	rows, err := i.db.QueryContext(ctx, `
		SELECT path
		FROM sessions
		WHERE deleted_at_ns IS NULL
		  AND (? = 'all' OR product = ?)
	`, string(target), string(target))
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var missing []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return 0, err
		}
		if !seen[path] {
			missing = append(missing, path)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, path := range missing {
		if _, err := i.db.ExecContext(ctx, `UPDATE sessions SET deleted_at_ns = ? WHERE path = ?`, deletedAt.UnixNano(), path); err != nil {
			return 0, err
		}
	}
	return len(missing), nil
}

func (i *sessionIndex) setMeta(ctx context.Context, key, value string) error {
	_, err := i.db.ExecContext(ctx, `
		INSERT INTO session_index_meta(key, value)
		VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}

func indexedRecentUnprocessedTargets(p paths, state *stateFile, target product, quietFor time.Duration, limit int) ([]sessionMeta, bool, error) {
	idx, err := openSessionIndex(p)
	if err != nil {
		return nil, false, err
	}
	defer idx.close()

	cutoff := time.Now().Add(-quietFor).UnixNano()
	out := make([]sessionMeta, 0, limit)
	offset := 0
	batchSize := limit * 20
	if batchSize < 50 {
		batchSize = 50
	}
	sawRows := false
	for len(out) < limit {
		rows, err := queryIndexedSessionBatch(idx.db, target, cutoff, batchSize, offset)
		if err != nil {
			return nil, false, err
		}
		var batch []indexedSession
		for rows.Next() {
			sawRows = true
			var s indexedSession
			var productName string
			var mtimeNS int64
			if err := rows.Scan(&productName, &s.sessionID, &s.filePath, &mtimeNS, &s.cwd); err != nil {
				rows.Close()
				return nil, false, err
			}
			s.product = product(productName)
			s.mtime = time.Unix(0, mtimeNS)
			batch = append(batch, s)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, false, err
		}
		rows.Close()
		if len(batch) == 0 {
			break
		}
		for _, s := range batch {
			meta := sessionMeta{
				product:   s.product,
				sessionID: s.sessionID,
				filePath:  s.filePath,
				mtime:     s.mtime,
				cwd:       s.cwd,
			}
			if processedSession(state, meta) {
				continue
			}
			out = append(out, meta)
			if len(out) >= limit {
				break
			}
		}
		offset += len(batch)
	}
	if !sawRows {
		warm, err := idx.hasFinishedRefresh()
		if err != nil {
			return nil, false, err
		}
		return nil, warm, nil
	}
	return out, true, nil
}

func queryIndexedSessionBatch(db *sql.DB, target product, cutoff int64, limit, offset int) (*sql.Rows, error) {
	if target == productAll {
		return db.Query(`
			SELECT product, session_id, path, mtime_ns, cwd
			FROM sessions
			WHERE deleted_at_ns IS NULL
			  AND mtime_ns <= ?
			ORDER BY mtime_ns DESC
			LIMIT ? OFFSET ?
		`, cutoff, limit, offset)
	}
	return db.Query(`
		SELECT product, session_id, path, mtime_ns, cwd
		FROM sessions
		WHERE product = ?
		  AND deleted_at_ns IS NULL
		  AND mtime_ns <= ?
		ORDER BY mtime_ns DESC
		LIMIT ? OFFSET ?
	`, string(target), cutoff, limit, offset)
}

func (i *sessionIndex) hasFinishedRefresh() (bool, error) {
	var value string
	err := i.db.QueryRow(`SELECT value FROM session_index_meta WHERE key = 'refresh_finished_at_ns'`).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return value != "", nil
}

func explainSessionIndex(p paths) (string, error) {
	idx, err := openSessionIndex(p)
	if err != nil {
		return "", err
	}
	defer idx.close()
	var value string
	err = idx.db.QueryRow(`SELECT value FROM session_index_meta WHERE key = 'refresh_finished_at_ns'`).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "index pending", nil
	}
	if err != nil {
		return "", err
	}
	ns, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return "", fmt.Errorf("parsing refresh_finished_at_ns: %w", err)
	}
	return "indexed " + time.Since(time.Unix(0, ns)).Round(time.Second).String() + " ago", nil
}
