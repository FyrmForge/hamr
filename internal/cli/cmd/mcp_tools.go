package cmd

// bridgeTool is one MCP tool the `hamr mcp` bridge can advertise. The bridge
// registers a tool only when the project's [dev.mcp.access] map exposes it; the
// dev-server gateway enforces the same permission again per call. inputSchema
// is a JSON Schema object describing the tool's arguments.
type bridgeTool struct {
	name        string
	description string
	inputSchema string
}

// noArgs is the input schema for a tool that takes no arguments.
const noArgs = `{"type":"object"}`

// bridgeTools is the full tool catalog, keyed by the same names the gateway's
// access map exposes (see internal/devserver/mcpconfig.go mcpAreas). Keep the
// two in sync.
var bridgeTools = []bridgeTool{
	{
		name:        "dev.info",
		description: "Snapshot of the controllable surface: resolved proxy URL + app port, watch rules with status, docker stacks, allowed make targets, current build errors, and the gateway's effective permissions. Call this first to discover valid rule/stack/target names.",
		inputSchema: noArgs,
	},
	{
		name:        "logs.read",
		description: "Read the dev server's process-output log buffer. Filter by watch-rule name and/or substring; tail returns the last N matching lines (default 200).",
		inputSchema: `{"type":"object","properties":{"rule":{"type":"string","description":"filter to one watch rule's output"},"contains":{"type":"string","description":"substring filter"},"tail":{"type":"integer","description":"last N matching lines (default 200)"}}}`,
	},
	{
		name:        "console.read",
		description: "Read browser-console output (client-side JS logs/errors) captured from the running app. Filter by level/substring; tail returns the last N lines.",
		inputSchema: `{"type":"object","properties":{"level":{"type":"string","description":"match on the rendered level tag, e.g. error"},"contains":{"type":"string"},"tail":{"type":"integer"}}}`,
	},
	{
		name:        "http.read",
		description: "Recent HTTP requests through the dev proxy (method, path, status, latency in ms) — including static assets, /__hamr/* endpoints, and SSE/WS, which the app's own logs don't show. Filter by method, path substring, and min_status (e.g. 400 to see only errors). Useful for verifying HTMX request/response flows.",
		inputSchema: `{"type":"object","properties":{"method":{"type":"string"},"path":{"type":"string","description":"substring match"},"min_status":{"type":"integer","description":"only status >= this, e.g. 400"},"tail":{"type":"integer","description":"last N (default 200)"}}}`,
	},
	{
		name:        "docker.logs",
		description: "Recent docker compose logs for a stack. name is required; optionally restrict to one service and filter by tail/since/substring.",
		inputSchema: `{"type":"object","properties":{"name":{"type":"string","description":"docker compose stack name"},"service":{"type":"string"},"tail":{"type":"integer","description":"default 100"},"since":{"type":"string","description":"compose duration, e.g. 5m"},"contains":{"type":"string"}},"required":["name"]}`,
	},
	{
		name:        "docker.status",
		description: "Container states (running/health) for a docker compose stack. name is required; optionally restrict to one service.",
		inputSchema: `{"type":"object","properties":{"name":{"type":"string"},"service":{"type":"string"}},"required":["name"]}`,
	},
	{
		name:        "docker.restart",
		description: "Restart a docker compose stack or a single service. Returns immediately by default (poll docker.status); pass wait:true to block until services are running/healthy and get the final statuses back.",
		inputSchema: `{"type":"object","properties":{"name":{"type":"string"},"service":{"type":"string"},"wait":{"type":"boolean","description":"block until running/healthy"},"wait_timeout":{"type":"string","description":"duration, default 60s"}},"required":["name"]}`,
	},
	{
		name:        "docker.wipe",
		description: "DESTRUCTIVE: docker compose down -v then up (drops volumes/data) for a stack or single service. Returns immediately by default; pass wait:true to block until services are running/healthy.",
		inputSchema: `{"type":"object","properties":{"name":{"type":"string"},"service":{"type":"string"},"wait":{"type":"boolean"},"wait_timeout":{"type":"string","description":"duration, default 60s"}},"required":["name"]}`,
	},
	{
		name:        "rule.run",
		description: "Run/reload one watch rule (rebuild a target). Enqueued onto the serialized scheduler — poll logs.read for output.",
		inputSchema: `{"type":"object","properties":{"name":{"type":"string","description":"watch rule name"}},"required":["name"]}`,
	},
	{
		name:        "rebuild.all",
		description: "Enqueue every watch rule for a full rebuild. Poll logs.read for output.",
		inputSchema: noArgs,
	},
	{
		name:        "make.run",
		description: "Run a Makefile target. Waits briefly for completion: returns status=done with exitCode+output for fast targets, or status=running for slow ones — then poll logs.read (rule make:<target>) for the completion marker.",
		inputSchema: `{"type":"object","properties":{"target":{"type":"string"}},"required":["target"]}`,
	},
	{
		name:        "mail.list",
		description: "List messages in the dev mail mock inbox (id, from, to, subject, date).",
		inputSchema: noArgs,
	},
	{
		name:        "mail.get",
		description: "Fetch one mail-mock message (headers, text, html) by id.",
		inputSchema: `{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`,
	},
	{
		name:        "mail.clear",
		description: "Empty the dev mail mock inbox.",
		inputSchema: noArgs,
	},
	{
		name:        "mail.ingest",
		description: "Inject a message into the mail mock inbox. Body is a message object (From, To, Subject, Text/HTML).",
		inputSchema: `{"type":"object","properties":{"Subject":{"type":"string"},"Text":{"type":"string"},"HTML":{"type":"string"}}}`,
	},
	{
		name:        "stripe.list",
		description: "Read-only snapshot of the Stripe mock state: sessions, payment intents, payouts, refunds, accounts (id/status/amount).",
		inputSchema: noArgs,
	},
	{
		name:        "stripe.complete",
		description: "Apply an outcome to an open checkout session in the Stripe mock, firing the matching webhooks.",
		inputSchema: `{"type":"object","properties":{"session":{"type":"string"},"outcome":{"type":"string","enum":["paid","failed","cancelled"]}},"required":["session","outcome"]}`,
	},
	{
		name:        "stripe.expire",
		description: "Expire an open checkout session in the Stripe mock (fires checkout.session.expired).",
		inputSchema: `{"type":"object","properties":{"session":{"type":"string"}},"required":["session"]}`,
	},
	{
		name:        "stripe.refund",
		description: "Refund a payment intent in the Stripe mock (fires charge.refunded). amount is in the smallest currency unit.",
		inputSchema: `{"type":"object","properties":{"payment_intent":{"type":"string"},"amount":{"type":"integer"},"reverse_transfer":{"type":"boolean"},"refund_application_fee":{"type":"boolean"}},"required":["payment_intent"]}`,
	},
}
