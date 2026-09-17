package devserver

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// devInfo assembles the controllable-surface snapshot the agent calls first.
func (g *mcpGateway) devInfo() devInfoResult {
	errSnap := g.errorState.Snapshot()
	statusFor := func(name string) string {
		if _, bad := errSnap[name]; bad {
			return "error"
		}
		return "ok"
	}

	rules := make([]devInfoRule, 0, len(g.cfg.Dev.Watch))
	for _, r := range g.cfg.Dev.Watch {
		rules = append(rules, devInfoRule{Name: r.Name, Status: statusFor(r.Name)})
	}

	stacks := make([]devInfoStack, 0, len(g.cfg.Dev.DockerCompose))
	for _, dc := range g.cfg.Dev.DockerCompose {
		stacks = append(stacks, devInfoStack{Name: dc.Name, Services: dc.Services, Status: statusFor(dc.Name)})
	}

	// Make targets, intersected with the make_targets allowlist when set.
	var makeTargets []string
	if targets, err := MakefileTargetsFromPath(g.makefile); err == nil {
		for _, t := range targets {
			if g.cfg.Dev.MCP.MakeTargetAllowed(t) {
				makeTargets = append(makeTargets, t)
			}
		}
	}

	errlist := make([]devInfoError, 0, len(errSnap))
	for src, msg := range errSnap {
		errlist = append(errlist, devInfoError{Source: src, Message: firstLines(msg, 5)})
	}

	mail := devInfoMail{Enabled: g.cfg.Dev.Email.Enabled}
	if mail.Enabled {
		mail.URL = g.proxyURL + "/__hamr/mail"
	}

	smsInfo := devInfoMail{Enabled: g.cfg.Dev.SMS.Enabled}
	if smsInfo.Enabled {
		smsInfo.URL = g.proxyURL + "/__hamr/sms"
	}

	return devInfoResult{
		ProxyURL:    g.proxyURL,
		AppPort:     g.appPort,
		Rules:       rules,
		Stacks:      stacks,
		MakeTargets: makeTargets,
		Errors:      errlist,
		Mail:        mail,
		SMS:         smsInfo,
		Stripe:      devInfoStripe{Mode: g.stripeModeName()},
		Gateway: devInfoGateway{
			Enabled: g.IsEnabled(),
			Access:  g.cfg.Dev.MCP.Access,
			Tools:   sortedTools(g.toolSet),
		},
	}
}

func sortedTools(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for t := range m {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// consoleRead returns recent browser-console frames (structured + timestamped)
// from the console sink's ring buffer.
func (g *mcpGateway) consoleRead(body []byte) (any, error) {
	var a consoleReadArgs
	if err := decodeArgs(body, &a); err != nil {
		return nil, err
	}
	if g.consoleSink == nil {
		return nil, fmt.Errorf("browser console capture is disabled ([dev].hamr_console_capture = false)")
	}
	tail := a.Tail
	if tail <= 0 {
		tail = 200
	}
	return g.consoleSink.Snapshot(a.Level, a.Contains, tail), nil
}

// httpRead returns recent proxy HTTP requests (method/path/status/latency),
// filterable by method, path substring, min status, and tail. This is the
// proxy-level view the app's own access log can't provide.
func (g *mcpGateway) httpRead(body []byte) (any, error) {
	var a httpReadArgs
	if err := decodeArgs(body, &a); err != nil {
		return nil, err
	}
	if g.requestLog == nil {
		return nil, fmt.Errorf("request logging unavailable (no proxy running)")
	}
	tail := a.Tail
	if tail <= 0 {
		tail = 200
	}
	entries := g.requestLog.Snapshot()
	out := make([]RequestLogEntry, 0, len(entries))
	for _, e := range entries {
		if a.Method != "" && !strings.EqualFold(e.Method, a.Method) {
			continue
		}
		if a.Path != "" && !strings.Contains(e.Path, a.Path) {
			continue
		}
		if a.MinStatus > 0 && e.Status < a.MinStatus {
			continue
		}
		out = append(out, e)
	}
	if len(out) > tail {
		out = out[len(out)-tail:]
	}
	return out, nil
}

// makeRun runs a Makefile target with a bounded wait, then degrades to "poll".
func (g *mcpGateway) makeRun(body []byte) (any, error) {
	var a makeRunArgs
	if err := decodeArgs(body, &a); err != nil {
		return nil, err
	}
	if a.Target == "" {
		return nil, fmt.Errorf("target is required")
	}
	if !g.cfg.Dev.MCP.MakeTargetAllowed(a.Target) {
		return nil, fmt.Errorf("make target %q not allowed by [dev.mcp] make_targets", a.Target)
	}
	// Runs through DevActions so the output lands in the shared log buffer and
	// on the event bus — same path the TUI's `m` hotkey takes. The run outlives
	// this request when it's slow; the agent polls logs.read for the completion
	// marker. Deliberately not cancelled on timeout.
	done, _ := g.actions.RunMake(a.Target)

	timer := time.NewTimer(g.cfg.Dev.MCP.ResolvedMakeWait())
	defer timer.Stop()
	select {
	case r := <-done:
		code := r.ExitCode
		return makeRunResult{Status: "done", ExitCode: &code, Output: r.Output}, nil
	case <-timer.C:
		return makeRunResult{
			Status:  "running",
			Message: fmt.Sprintf("still running — poll logs.read for rule %q", "make:"+a.Target),
		}, nil
	}
}

// auditOutcome makes a make.run failure visible in the audit log: a target that
// finished within the wait window records "done exit=N" (so a non-zero exit
// reads as a failure, not "ok"); a still-running one records "running".
func (r makeRunResult) auditOutcome() string {
	if r.Status == "running" {
		return "running"
	}
	if r.ExitCode != nil {
		return fmt.Sprintf("done exit=%d", *r.ExitCode)
	}
	return "done"
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}

func tailString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// --- mail ---

func (g *mcpGateway) mailList() []mailSummary {
	if g.mailMock == nil {
		return []mailSummary{}
	}
	msgs := g.mailMock.List()
	out := make([]mailSummary, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, mailSummary{
			ID:      m.ID,
			From:    m.From.Email,
			To:      joinAddrs(m.To),
			Subject: m.Subject,
			Date:    m.ReceivedAt.Format(time.RFC3339),
		})
	}
	return out
}

func (g *mcpGateway) mailGet(body []byte) (any, error) {
	if g.mailMock == nil {
		return nil, fmt.Errorf("mail mock not enabled")
	}
	var a mailGetArgs
	if err := decodeArgs(body, &a); err != nil {
		return nil, err
	}
	m := g.mailMock.Get(a.ID)
	if m == nil {
		return nil, fmt.Errorf("message %q not found", a.ID)
	}
	return mailDetail{
		ID:      m.ID,
		From:    m.From.Email,
		To:      joinAddrs(m.To),
		Subject: m.Subject,
		Date:    m.ReceivedAt.Format(time.RFC3339),
		Text:    m.Text,
		HTML:    m.HTML,
		Headers: m.Headers,
	}, nil
}

func (g *mcpGateway) mailIngest(body []byte) (any, error) {
	if g.mailMock == nil {
		return nil, fmt.Errorf("mail mock not enabled")
	}
	var msg mailMessage
	if err := decodeArgs(body, &msg); err != nil {
		return nil, fmt.Errorf("invalid message: %w", err)
	}
	msg.ID = newMessageID()
	msg.ReceivedAt = time.Now()
	msg.Status = "delivered"
	g.mailMock.append(&msg)
	return mailIngestResult{ID: msg.ID}, nil
}

// --- sms ---

func (g *mcpGateway) smsList() []smsSummary {
	if g.smsMock == nil {
		return []smsSummary{}
	}
	msgs := g.smsMock.List()
	out := make([]smsSummary, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, smsSummary{
			ID:   m.ID,
			From: m.From,
			To:   m.To,
			Body: m.Body,
			Date: m.ReceivedAt.Format(time.RFC3339),
		})
	}
	return out
}

func (g *mcpGateway) smsGet(body []byte) (any, error) {
	if g.smsMock == nil {
		return nil, fmt.Errorf("sms mock not enabled")
	}
	var a mailGetArgs
	if err := decodeArgs(body, &a); err != nil {
		return nil, err
	}
	m := g.smsMock.Get(a.ID)
	if m == nil {
		return nil, fmt.Errorf("message %q not found", a.ID)
	}
	return smsSummary{
		ID:   m.ID,
		From: m.From,
		To:   m.To,
		Body: m.Body,
		Date: m.ReceivedAt.Format(time.RFC3339),
	}, nil
}

func (g *mcpGateway) smsIngest(body []byte) (any, error) {
	if g.smsMock == nil {
		return nil, fmt.Errorf("sms mock not enabled")
	}
	var msg smsMessage
	if err := decodeArgs(body, &msg); err != nil {
		return nil, fmt.Errorf("invalid message: %w", err)
	}
	msg.ID = newSMSMessageID()
	msg.ReceivedAt = time.Now()
	msg.Status = "delivered"
	g.smsMock.append(&msg)
	return mailIngestResult{ID: msg.ID}, nil
}

func joinAddrs(addrs []mailAddress) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		parts = append(parts, a.Email)
	}
	return strings.Join(parts, ", ")
}

// --- stripe ---

// stripeModeName is the running Stripe mode: "off", "mock" or "listen".
func (g *mcpGateway) stripeModeName() string {
	if g.stripe == nil {
		return StripeModeOff
	}
	if mode := g.stripe.Mode(); mode != "" {
		return mode
	}
	return g.cfg.Dev.Stripe.ResolvedMode() // still booting
}

func (g *mcpGateway) stripeMode(body []byte) (any, error) {
	if g.stripe == nil {
		return nil, fmt.Errorf("stripe is off ([dev.stripe] mode)")
	}
	var a stripeModeArgs
	if err := decodeArgs(body, &a); err != nil {
		return nil, err
	}
	mode, err := g.stripe.Set(a.Mode)
	if err != nil {
		return nil, fmt.Errorf("%w (running: %s)", err, mode)
	}
	return stripeModeResult{Mode: mode}, nil
}

func (g *mcpGateway) stripeList() (any, error) {
	if g.stripeMock == nil {
		return nil, fmt.Errorf("stripe mock not enabled")
	}
	return g.stripeMock.stateSummary(), nil
}

func (g *mcpGateway) stripeComplete(body []byte) (any, error) {
	if g.stripeMock == nil {
		return nil, fmt.Errorf("stripe mock not enabled")
	}
	var a stripeCompleteArgs
	if err := decodeArgs(body, &a); err != nil {
		return nil, err
	}
	if a.Session == "" {
		return nil, fmt.Errorf("session is required")
	}
	if _, _, err := g.stripeMock.completeCheckout(a.Session, a.Outcome); err != nil {
		return nil, err
	}
	return okResult{OK: true}, nil
}

func (g *mcpGateway) stripeExpire(body []byte) (any, error) {
	if g.stripeMock == nil {
		return nil, fmt.Errorf("stripe mock not enabled")
	}
	var a stripeExpireArgs
	if err := decodeArgs(body, &a); err != nil {
		return nil, err
	}
	if a.Session == "" {
		return nil, fmt.Errorf("session is required")
	}
	if err := g.stripeMock.expireSession(a.Session); err != nil {
		return nil, err
	}
	return okResult{OK: true}, nil
}

func (g *mcpGateway) stripeRefund(body []byte) (any, error) {
	if g.stripeMock == nil {
		return nil, fmt.Errorf("stripe mock not enabled")
	}
	var a stripeRefundArgs
	if err := decodeArgs(body, &a); err != nil {
		return nil, err
	}
	if a.PaymentIntent == "" {
		return nil, fmt.Errorf("payment_intent is required")
	}
	rf, err := g.stripeMock.refundPayment(a.PaymentIntent, a.Amount, a.ReverseTransfer, a.RefundAppFee)
	if err != nil {
		return nil, err
	}
	return stripeRefundResult{ID: rf.ID, Amount: rf.Amount, Status: rf.Status}, nil
}
