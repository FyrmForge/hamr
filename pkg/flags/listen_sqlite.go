package flags

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

const sqlitePollEvery = time.Second

// WithSQLite watches db by polling PRAGMA data_version on one pinned
// connection and reloads when another connection has committed.
// ponytail: data_version ignores this connection's own commits (Set updates
// the cache itself) and a 1s poll means up to 1s staleness. With a
// single-connection pool (hamr's default) the connection is borrowed per
// tick instead of pinned, since pinning would starve the app.
func WithSQLite(db *sql.DB) Option {
	return func(c *Client) { c.watch = func(ctx context.Context, c *Client) { watchSQLite(ctx, c, db) } }
}

func watchSQLite(ctx context.Context, c *Client, db *sql.DB) {
	log := c.log
	if log == nil {
		log = slog.Default()
	}
	var conn *sql.Conn
	defer func() {
		if conn != nil {
			_ = conn.Close()
		}
	}()
	last, haveLast := int64(0), false
	t := time.NewTicker(sqlitePollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if conn == nil {
			cn, err := db.Conn(ctx)
			if err != nil {
				continue
			}
			conn = cn
		}
		var v int64
		if err := conn.QueryRowContext(ctx, "PRAGMA data_version").Scan(&v); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Debug("flags: data_version failed", "error", err)
			_ = conn.Close()
			conn, haveLast = nil, false
			continue
		}
		changed := haveLast && v != last
		last, haveLast = v, true
		// Release before reload: on a single-connection pool, reload's List
		// needs the connection this loop is holding.
		if db.Stats().MaxOpenConnections == 1 {
			_ = conn.Close()
			conn = nil
		}
		if changed {
			_ = c.reload(ctx)
		}
	}
}
