package websocket

// TODO: Implement PG-backed hub for cross-instance WebSocket routing.
//
// Use an UNLOGGED table (like the rate-limit PGStore) to track active
// sessions and leverage PostgreSQL LISTEN/NOTIFY for cross-instance
// message fan-out.
//
// Key design points:
//   - Register/unregister write session rows (session_id, subject_id, instance_id)
//   - SendToSession/SendToSubject/SendToRoom use NOTIFY to reach other instances
//   - Each instance runs a LISTEN goroutine that forwards messages to local clients
//   - Stale rows cleaned up via keep-alive or TTL
