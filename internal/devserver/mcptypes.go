package devserver

// This file defines the request (args) and response types for every MCP tool
// the gateway dispatches. Tools decode their args into the *Args structs and
// return the typed result structs — no map[string]any in the dispatch path.

// --- generic results ---

// okResult is the ack returned by fire-and-forget tools.
type okResult struct {
	OK bool `json:"ok"`
}

// --- dev.info ---

type devInfoResult struct {
	ProxyURL    string         `json:"proxyURL"`
	AppPort     int            `json:"appPort"`
	Rules       []devInfoRule  `json:"rules"`
	Stacks      []devInfoStack `json:"stacks"`
	MakeTargets []string       `json:"makeTargets"`
	Errors      []devInfoError `json:"errors"`
	Mail        devInfoMail    `json:"mail"`
	SMS         devInfoMail    `json:"sms"`
	Stripe      devInfoStripe  `json:"stripe"`
	Gateway     devInfoGateway `json:"gateway"`
}

type devInfoRule struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "ok" | "error"
}

type devInfoStack struct {
	Name     string   `json:"name"`
	Services []string `json:"services"`
	Status   string   `json:"status"` // "ok" | "error"
}

type devInfoError struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

type devInfoMail struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url,omitempty"`
}

type devInfoStripe struct {
	Enabled bool `json:"enabled"`
}

type devInfoGateway struct {
	Enabled bool              `json:"enabled"`
	Access  map[string]string `json:"access"`
	Tools   []string          `json:"tools"`
}

// --- logs / console ---

type logsReadArgs struct {
	Rule     string `json:"rule"`
	Contains string `json:"contains"`
	Tail     int    `json:"tail"`
}

type consoleReadArgs struct {
	Level    string `json:"level"`
	Contains string `json:"contains"`
	Tail     int    `json:"tail"`
}

// logEntry is one line returned by logs.read: ANSI-stripped text with its
// rule tag and an RFC3339 timestamp (for correlating with console.read).
type logEntry struct {
	Time string `json:"time"`
	Rule string `json:"rule"`
	Text string `json:"text"`
}

// httpReadArgs filters the proxy request log for http.read.
type httpReadArgs struct {
	Method    string `json:"method"`     // exact method filter (case-insensitive)
	Path      string `json:"path"`       // substring match on path
	MinStatus int    `json:"min_status"` // only entries with status >= this (e.g. 400 for errors)
	Tail      int    `json:"tail"`       // last N matches, default 200
}

// consoleLine is one browser-console frame returned by console.read.
type consoleLine struct {
	Time  string `json:"time"`
	Level string `json:"level,omitempty"`
	Msg   string `json:"msg"`
	Src   string `json:"src,omitempty"`
}

// --- docker ---

type dockerStatusArgs struct {
	Name    string `json:"name"`
	Service string `json:"service"`
}

type dockerLogsArgs struct {
	Name     string `json:"name"`
	Service  string `json:"service"`
	Since    string `json:"since"`
	Contains string `json:"contains"`
	Tail     int    `json:"tail"`
}

type dockerLogsResult struct {
	Output string `json:"output"`
}

type dockerActionArgs struct {
	Name        string `json:"name"`
	Service     string `json:"service"`
	Wait        bool   `json:"wait"`         // block until services are running/healthy
	WaitTimeout string `json:"wait_timeout"` // duration string; default 60s when wait is set
}

// dockerWaitResult is returned by docker.restart/wipe when wait is set: the
// final container statuses and whether everything reached running/healthy
// before the timeout.
type dockerWaitResult struct {
	OK       bool              `json:"ok"`
	Healthy  bool              `json:"healthy"`
	Statuses []containerStatus `json:"statuses"`
}

// --- build ---

type ruleRunArgs struct {
	Name string `json:"name"`
}

type makeRunArgs struct {
	Target string `json:"target"`
}

type makeRunResult struct {
	Status   string `json:"status"` // "done" | "running"
	ExitCode *int   `json:"exitCode,omitempty"`
	Output   string `json:"output,omitempty"`
	Message  string `json:"message,omitempty"`
}

// --- mail ---

type mailGetArgs struct {
	ID string `json:"id"`
}

type mailSummary struct {
	ID      string `json:"id"`
	From    string `json:"from"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	Date    string `json:"date"`
}

// mailIngestResult is the ack for mail.ingest: the id of the injected message.
type mailIngestResult struct {
	ID string `json:"id"`
}

type mailDetail struct {
	ID      string            `json:"id"`
	From    string            `json:"from"`
	To      string            `json:"to"`
	Subject string            `json:"subject"`
	Date    string            `json:"date"`
	Text    string            `json:"text"`
	HTML    string            `json:"html"`
	Headers map[string]string `json:"headers"`
}

// --- sms ---

// smsSummary doubles as the sms.get detail — an SMS has no fields beyond
// these, so list and get share one shape.
type smsSummary struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
	Body string `json:"body"`
	Date string `json:"date"`
}

// --- stripe ---

type stripeCompleteArgs struct {
	Session string `json:"session"`
	Outcome string `json:"outcome"` // paid | failed | cancelled
}

type stripeExpireArgs struct {
	Session string `json:"session"`
}

type stripeRefundArgs struct {
	PaymentIntent   string `json:"payment_intent"`
	Amount          int64  `json:"amount"`
	ReverseTransfer bool   `json:"reverse_transfer"`
	RefundAppFee    bool   `json:"refund_application_fee"`
}

// stripeRefundResult is the ack returned by stripe.refund.
type stripeRefundResult struct {
	ID     string `json:"id"`
	Amount int64  `json:"amount"`
	Status string `json:"status"`
}

// StripeStateSummary is the read-only snapshot returned by stripe.list.
type StripeStateSummary struct {
	Sessions       []StripeSessionSummary `json:"sessions"`
	PaymentIntents []StripeObjectSummary  `json:"paymentIntents"`
	Payouts        []StripeObjectSummary  `json:"payouts"`
	Refunds        []StripeObjectSummary  `json:"refunds"`
	Accounts       []StripeAccountSummary `json:"accounts"`
}

type StripeObjectSummary struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

// StripeSessionSummary adds the hosted-checkout URL and line items so an agent
// can verify it's acting on the right session before completing/expiring it.
type StripeSessionSummary struct {
	ID        string                  `json:"id"`
	Status    string                  `json:"status"`
	Amount    int64                   `json:"amount"`
	Currency  string                  `json:"currency"`
	URL       string                  `json:"url"`
	LineItems []StripeLineItemSummary `json:"lineItems,omitempty"`
}

type StripeLineItemSummary struct {
	Name       string `json:"name"`
	UnitAmount int64  `json:"unitAmount"`
	Quantity   int64  `json:"quantity"`
}

type StripeAccountSummary struct {
	ID             string `json:"id"`
	Email          string `json:"email"`
	ChargesEnabled bool   `json:"chargesEnabled"`
}
