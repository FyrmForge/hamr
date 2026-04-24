package devserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// stripeStateFile is the on-disk representation of the StripeMock's
// in-memory state. Single JSON document, atomically rewritten on every
// state mutation. Mirrors the email mock's mbox-based persistence but uses
// JSON because Stripe state is plain Go structs (no MIME equivalent).
//
// Schema is intentionally non-versioned: encoding/json silently ignores
// unknown fields and zeroes missing ones, so adding fields to the state
// structs is forward-compatible. Removing or renaming a field requires a
// migration — easier to just `rm .hamr/stripe/state.json` in dev.
type stripeStateFile struct {
	Sessions       map[string]*stripeSession       `json:"sessions"`
	Accounts       map[string]*stripeAccount       `json:"accounts"`
	PaymentIntents map[string]*stripePaymentIntent `json:"payment_intents"`
	Charges        map[string]*stripeCharge        `json:"charges"`
	Transfers      map[string]*stripeTransfer      `json:"transfers"`
	Refunds        map[string]*stripeRefund        `json:"refunds"`
	Payouts        map[string]*stripePayout        `json:"payouts"`
}

// persist serializes the entire in-memory state and atomically writes it
// to persistPath. No-op when persistPath is empty. Errors are reported via
// the persistErr callback (typically a slog.Warn) rather than surfaced to
// the caller — persistence failure should never block a successful API
// response or webhook.
//
// Caller must hold m.mu (write lock) so the snapshot is consistent.
func (m *StripeMock) persist() {
	if m.persistPath == "" {
		return
	}
	state := stripeStateFile{
		Sessions:       m.sessions,
		Accounts:       m.accounts,
		PaymentIntents: m.paymentIntents,
		Charges:        m.charges,
		Transfers:      m.transfers,
		Refunds:        m.refunds,
		Payouts:        m.payouts,
	}
	if err := writeStripeState(m.persistPath, state); err != nil {
		m.reportPersistErr(err)
	}
}

// reportPersistErr forwards a persistence error to the configured callback.
// Silently dropped if no callback was set at construction time.
func (m *StripeMock) reportPersistErr(err error) {
	if m.persistErr != nil {
		m.persistErr(err)
	}
}

// loadFromDisk reads the persist file and populates the in-memory maps.
// Missing file is not an error (first run). Corrupt file is reported via
// reportPersistErr and the inbox starts empty. Maps that are nil after
// unmarshal (because the file predates a particular resource) are
// re-initialized so write-side code can blindly index them.
//
// Caller must NOT hold m.mu (called from NewStripeMock before any
// concurrent access is possible).
func (m *StripeMock) loadFromDisk() {
	if m.persistPath == "" {
		return
	}
	data, err := os.ReadFile(m.persistPath)
	if err != nil {
		if os.IsNotExist(err) {
			return // first run; nothing to load
		}
		m.reportPersistErr(fmt.Errorf("read stripe state: %w", err))
		return
	}
	if len(data) == 0 {
		return
	}

	var state stripeStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		m.reportPersistErr(fmt.Errorf("decode stripe state: %w", err))
		return
	}

	if state.Sessions != nil {
		m.sessions = state.Sessions
	}
	if state.Accounts != nil {
		m.accounts = state.Accounts
	}
	if state.PaymentIntents != nil {
		m.paymentIntents = state.PaymentIntents
	}
	if state.Charges != nil {
		m.charges = state.Charges
	}
	if state.Transfers != nil {
		m.transfers = state.Transfers
	}
	if state.Refunds != nil {
		m.refunds = state.Refunds
	}
	if state.Payouts != nil {
		m.payouts = state.Payouts
	}
}

// writeStripeState atomically writes the state to path via tmp + rename.
// Mirrors mailmock_mbox.go writeMboxInbox: tmp file in the same directory
// as path (so rename is atomic on the same filesystem), then rename. A
// crash mid-write leaves the prior persist file intact.
func writeStripeState(path string, state stripeStateFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("stripemock: mkdir persist dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".stripe-state.*.json.tmp")
	if err != nil {
		return fmt.Errorf("stripemock: create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) //nolint:errcheck

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(state); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("stripemock: encode state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("stripemock: close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("stripemock: rename tmp: %w", err)
	}
	return nil
}
