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
	Mode string `json:"mode"` // "off" | "mock" | "listen"
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
	Session       string `json:"session"`
	Outcome       string `json:"outcome"`                  // paid | failed | cancelled
	PromotionCode string `json:"promotion_code,omitempty"` // what a buyer would type on the hosted page; paid only
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

// stripeModeArgs is stripe.mode's input. Empty Mode flips mock ⇄ listen.
type stripeModeArgs struct {
	Mode string `json:"mode"`
}

// stripeModeResult is the mode running after stripe.mode.
type stripeModeResult struct {
	Mode string `json:"mode"`
}

// stripeRefundResult is the ack returned by stripe.refund.
type stripeRefundResult struct {
	ID     string `json:"id"`
	Amount int64  `json:"amount"`
	Status string `json:"status"`
}

// stripeAdvanceArgs is stripe.advance's input: exactly one of by (1d, 2w,
// 1m, 1y or a Go duration) or to (RFC 3339 or unix seconds).
type stripeAdvanceArgs struct {
	By string `json:"by"`
	To string `json:"to"`
}

// stripeAdvanceResult reports the clock after stripe.advance and every
// subscription the advance acted on (renewed or ended).
type stripeAdvanceResult struct {
	Now    string   `json:"now"`
	Cycled []string `json:"cycled"`
}

// stripeSubscriptionArgs is stripe.subscription's input.
type stripeSubscriptionArgs struct {
	Subscription string `json:"subscription"`
	Action       string `json:"action"` // next | retry | fail_next | cancel
}

// stripeSubscriptionResult is the subscription's status after the action.
type stripeSubscriptionResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// StripeStateSummary is the read-only snapshot returned by stripe.list.
type StripeStateSummary struct {
	Clock          string                      `json:"clock"` // the mock clock, RFC 3339
	Sessions       []StripeSessionSummary      `json:"sessions"`
	Subscriptions  []StripeSubscriptionSummary `json:"subscriptions"`
	PaymentIntents []StripeObjectSummary       `json:"paymentIntents"`
	Payouts        []StripeObjectSummary       `json:"payouts"`
	Refunds        []StripeObjectSummary       `json:"refunds"`
	Accounts       []StripeAccountSummary      `json:"accounts"`
	Coupons        []StripeCouponSummary       `json:"coupons"`
	Customers      []StripeCustomerSummary     `json:"customers"`
	Prices         []StripePriceSummary        `json:"prices"`
}

// StripeCustomerSummary is one customer and how many subscriptions it holds.
type StripeCustomerSummary struct {
	ID            string `json:"id"`
	Email         string `json:"email,omitempty"`
	Name          string `json:"name,omitempty"`
	Subscriptions int    `json:"subscriptions"`
}

// StripePriceSummary is one price, so an agent can pick an id for
// line_items[].price at checkout.
type StripePriceSummary struct {
	ID        string `json:"id"`
	Product   string `json:"product"` // the product's name
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Interval  string `json:"interval,omitempty"` // empty for a one-time price
	LookupKey string `json:"lookupKey,omitempty"`
	Active    bool   `json:"active"`
}

// StripeSubscriptionSummary is one subscription: what it bills per period
// and when the mock clock next acts on it.
type StripeSubscriptionSummary struct {
	ID                string `json:"id"`
	Status            string `json:"status"`
	Customer          string `json:"customer"`
	Amount            int64  `json:"amount"` // per period, before discount
	Currency          string `json:"currency"`
	Interval          string `json:"interval"`
	PeriodEnd         string `json:"periodEnd"`
	CancelAtPeriodEnd bool   `json:"cancelAtPeriodEnd,omitempty"`
	FailNextRenewal   bool   `json:"failNextRenewal,omitempty"`
}

// StripeCouponSummary is one coupon with its active promotion codes, so an
// agent can pick a code to type at checkout.
type StripeCouponSummary struct {
	ID            string   `json:"id"`
	Name          string   `json:"name,omitempty"`
	PercentOff    float64  `json:"percentOff,omitempty"`
	AmountOff     int64    `json:"amountOff,omitempty"`
	Currency      string   `json:"currency,omitempty"`
	Duration      string   `json:"duration"`
	TimesRedeemed int64    `json:"timesRedeemed"`
	Codes         []string `json:"codes"`
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
	ChargesEnabled bool   `json:"chargesEnabled"`      // v1 accounts
	V2             bool   `json:"v2,omitempty"`        // Accounts v2 (/v2/core/accounts)
	Onboarded      bool   `json:"onboarded,omitempty"` // v2: every requested capability active
}
