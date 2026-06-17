package devserver

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStripeMock_Persist_RoundTrip writes a sample state via every
// resource's create/outcome path, then constructs a second mock pointed at
// the same persist file and verifies all state is restored. This is the
// canonical "did persistence actually capture everything?" test.
func TestStripeMock_Persist_RoundTrip(t *testing.T) {
	persistPath := filepath.Join(t.TempDir(), "state.json")
	mock, _, _ := newFullStripeStack(t, persistPath)

	// Seed every resource type the mock tracks. Seed helpers call
	// mock.persist() themselves (mirroring the real handlers), so the file
	// is in sync after each seed.
	acctID := seedConnectedAccount(t, mock)
	piID := seedPaymentIntent(t, mock, paymentIntentSeed{
		Amount: 5000, Currency: "gbp", ApplicationFeeAmount: 500, TransferDestination: acctID,
	})
	poID := seedPayout(t, mock, acctID)
	sessID := seedCheckoutSession(t, mock)
	// Drive the PI through a successful outcome so charges + transfers + a
	// completed PI all end up in the persist file. This goes through the
	// real handler so persist fires automatically.
	resp := postPaymentIntentComplete(t, mock, piID, "succeed")
	resp.Body.Close() //nolint:errcheck
	// Issue a refund to populate the refunds map + mutate the charge.
	_, _, _, err := mock.applyRefund(refundInput{
		piID:            piID,
		amount:          1000,
		reverseTransfer: true,
	})
	require.NoError(t, err)
	// Drive the payout outcome.
	resp = postPayoutComplete(t, mock, poID, "paid")
	resp.Body.Close() //nolint:errcheck

	// Verify the file exists + is valid JSON.
	data, err := os.ReadFile(persistPath)
	require.NoError(t, err)
	require.NotEmpty(t, data)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	for _, key := range []string{"sessions", "accounts", "payment_intents", "charges", "transfers", "refunds", "payouts"} {
		assert.Contains(t, raw, key, "persist file should contain all resource maps even if empty")
	}

	// Construct a fresh mock pointed at the same path and verify everything
	// round-tripped.
	restored := NewStripeMock(StripeMockOptions{
		BaseURL:     "http://restored.test",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		PersistPath: persistPath,
	})

	restored.mu.RLock()
	defer restored.mu.RUnlock()

	require.Contains(t, restored.accounts, acctID)
	assert.Equal(t, "express", restored.accounts[acctID].Type)
	assert.True(t, restored.accounts[acctID].ChargesEnabled)

	require.Contains(t, restored.paymentIntents, piID)
	pi := restored.paymentIntents[piID]
	assert.Equal(t, int64(5000), pi.Amount)
	assert.Equal(t, "succeeded", pi.Status)
	assert.NotEmpty(t, pi.LatestChargeID, "PI succeed cascade must have populated latest_charge_id pre-persist")

	require.Contains(t, restored.charges, pi.LatestChargeID)
	ch := restored.charges[pi.LatestChargeID]
	assert.Equal(t, int64(5000), ch.Amount)
	assert.Equal(t, int64(1000), ch.AmountRefunded, "refund mutation must persist")

	// Both the cascade-created Transfer and the refund's reversal should
	// round-trip cleanly.
	assert.NotEmpty(t, ch.TransferID)
	require.Contains(t, restored.transfers, ch.TransferID)
	tr := restored.transfers[ch.TransferID]
	assert.Equal(t, int64(4500), tr.Amount, "transfer = pi.amount - app_fee = 5000 - 500")
	assert.Equal(t, int64(1000), tr.AmountReversed, "reverse_transfer must persist")

	require.Len(t, restored.refunds, 1)

	require.Contains(t, restored.payouts, poID)
	assert.Equal(t, "paid", restored.payouts[poID].Status)

	require.Contains(t, restored.sessions, sessID)
}

// TestStripeMock_Persist_NoPathIsNoOp confirms that constructing without a
// PersistPath leaves the in-memory state ephemeral — no file ever appears
// and persist() is a silent no-op.
func TestStripeMock_Persist_NoPathIsNoOp(t *testing.T) {
	// OnPersistError must never fire on the no-path path — any invocation is a
	// bug (persist should bail before touching the filesystem).
	mock := NewStripeMock(StripeMockOptions{
		BaseURL: "http://x",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnPersistError: func(err error) {
			t.Errorf("OnPersistError fired with no PersistPath set: %v", err)
		},
	})
	require.Empty(t, mock.persistPath, "construction without PersistPath must stay in-memory only")

	// Seeding mutates state and triggers persist() internally; an explicit call
	// exercises the no-op path directly. Neither should error or write anything.
	acctID := seedConnectedAccount(t, mock)
	mock.mu.Lock()
	mock.persist()
	mock.mu.Unlock()

	// State is held in memory despite no persistence.
	mock.mu.RLock()
	_, ok := mock.accounts[acctID]
	mock.mu.RUnlock()
	assert.True(t, ok, "seeded account must live in memory when persistence is disabled")
}

// TestStripeMock_Persist_LoadCorruptFile_StartsEmptyAndReports asserts the
// safety promise: a corrupt file never crashes the mock, and the
// OnPersistError callback fires so dev sees the failure in `hamr dev` logs.
func TestStripeMock_Persist_LoadCorruptFile_StartsEmptyAndReports(t *testing.T) {
	persistPath := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(persistPath, []byte("{not json"), 0o644))

	var captured []error
	mock := NewStripeMock(StripeMockOptions{
		BaseURL:     "http://x",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		PersistPath: persistPath,
		OnPersistError: func(err error) {
			captured = append(captured, err)
		},
	})

	require.Len(t, captured, 1, "corrupt file should fire OnPersistError exactly once")
	assert.Contains(t, captured[0].Error(), "decode stripe state")
	assert.Empty(t, mock.sessions)
	assert.Empty(t, mock.accounts)
}

// TestStripeMock_Persist_MissingFileIsSilent confirms first-run behavior:
// no file = no error, just an empty mock ready to write.
func TestStripeMock_Persist_MissingFileIsSilent(t *testing.T) {
	persistPath := filepath.Join(t.TempDir(), "doesnotexist.json")

	var captured []error
	NewStripeMock(StripeMockOptions{
		BaseURL:     "http://x",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		PersistPath: persistPath,
		OnPersistError: func(err error) {
			captured = append(captured, err)
		},
	})

	assert.Empty(t, captured, "missing file is the first-run case, not an error")
}

// TestStripeMock_Persist_AtomicWrite verifies there's no half-written file
// even if many writes happen in quick succession — by the end of all the
// mutations the file is valid JSON. (We can't easily simulate a crash
// mid-write in unit tests; this is the closest reachable assertion.)
func TestStripeMock_Persist_AtomicWrite(t *testing.T) {
	persistPath := filepath.Join(t.TempDir(), "state.json")
	mock, _, _ := newFullStripeStack(t, persistPath)

	for range 20 {
		seedConnectedAccount(t, mock)
	}

	data, err := os.ReadFile(persistPath)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw), "file must always be valid JSON")
}

// --- helpers ---

// seedCheckoutSession inserts a checkout session directly into the mock so
// persist tests can verify it round-trips. Mirrors the helper-style of
// seedConnectedAccount and seedPaymentIntent.
func seedCheckoutSession(t *testing.T, mock *StripeMock) string {
	t.Helper()
	id := "cs_test_" + randomHex(16)
	mock.mu.Lock()
	mock.sessions[id] = &stripeSession{
		ID:              id,
		PaymentIntentID: "pi_test_" + randomHex(16),
		Mode:            "payment",
		Currency:        "gbp",
		AmountTotal:     2500,
		LineItems: []stripeLineItem{
			{Name: "Item", UnitAmount: 2500, Quantity: 1, Currency: "gbp"},
		},
		SuccessURL:    "https://app.example/success",
		CancelURL:     "https://app.example/cancel",
		Status:        "open",
		PaymentStatus: "unpaid",
	}
	mock.persist()
	mock.mu.Unlock()
	return id
}
