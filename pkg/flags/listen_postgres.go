package flags

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Table is the flags table the scaffold creates.
const Table = "feature_flags"

// Channel is the Postgres NOTIFY channel the trigger publishes on.
const Channel = "hamr_feature_flags"

// PostgresTriggerSQL creates the trigger that NOTIFYs Channel on every
// change to Table. It is idempotent and multi-statement. The scaffold's
// migration inlines the same SQL.
const PostgresTriggerSQL = `CREATE OR REPLACE FUNCTION hamr_feature_flags_notify() RETURNS trigger AS $$
BEGIN
	PERFORM pg_notify('` + Channel + `', '');
	RETURN NULL;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS hamr_feature_flags_notify ON ` + Table + `;
CREATE TRIGGER hamr_feature_flags_notify
	AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON ` + Table + `
	FOR EACH STATEMENT EXECUTE FUNCTION hamr_feature_flags_notify();`

var errNotPgx = errors.New("not a pgx connection")

// WithPostgres watches db (a pgx-backed *sql.DB) with LISTEN and reloads on
// every change. The listener pins one pooled connection, so the pool needs
// at least two (or use WithDirectURL); on a one-connection pool it logs an
// error and does not listen, leaving only the poll.
func WithPostgres(db *sql.DB) Option {
	return func(c *Client) { c.watch = func(ctx context.Context, c *Client) { watchPostgres(ctx, c, db) } }
}

// WithDirectURL makes the Postgres watcher open its own connection to url
// instead of using the pool. Use it behind PgBouncer in transaction mode,
// where LISTEN does not work.
func WithDirectURL(url string) Option { return func(c *Client) { c.directURL = url } }

func watchPostgres(ctx context.Context, c *Client, db *sql.DB) {
	log := c.log
	if log == nil {
		log = slog.Default()
	}
	// Pinning the only pooled connection would starve reload (and the app).
	if c.directURL == "" && db.Stats().MaxOpenConnections == 1 {
		log.Error("flags: pool has one connection, not listening (poll only); raise the pool or use WithDirectURL")
		return
	}
	const minBackoff, maxBackoff = time.Second, 30 * time.Second
	backoff, failures := minBackoff, 0
	for ctx.Err() == nil {
		listening := false
		err := listenOnce(ctx, c, db, func() { listening = true })
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errNotPgx) {
			log.Error("flags: db driver is not pgx, not listening (poll only)")
			return
		}
		if listening {
			backoff, failures = minBackoff, 0
		}
		// Warn on the first failure of a streak, then stay quiet.
		if failures++; failures == 1 {
			log.Warn("flags: listen lost, retrying", "error", err)
		} else {
			log.Debug("flags: listen still failing", "error", err, "attempt", failures)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// listenOnce listens on one connection until it errors. up is called after
// LISTEN succeeds.
func listenOnce(ctx context.Context, c *Client, db *sql.DB, up func()) error {
	if c.directURL != "" {
		pc, err := pgx.Connect(ctx, c.directURL)
		if err != nil {
			return err
		}
		defer func() { _ = pc.Close(context.Background()) }()
		return waitLoop(ctx, c, pc, up)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	return conn.Raw(func(dc any) error {
		sc, ok := dc.(*stdlib.Conn)
		if !ok {
			return errNotPgx
		}
		return waitLoop(ctx, c, sc.Conn(), up)
	})
}

func waitLoop(ctx context.Context, c *Client, pc *pgx.Conn, up func()) error {
	if _, err := pc.Exec(ctx, "LISTEN "+Channel); err != nil {
		return err
	}
	up()
	// Reload after LISTEN: notifies sent while disconnected were lost.
	// ponytail: a reload error is only retried by the next notify or poll.
	_ = c.reload(ctx)
	for {
		if _, err := pc.WaitForNotification(ctx); err != nil {
			return err
		}
		_ = c.reload(ctx)
	}
}
