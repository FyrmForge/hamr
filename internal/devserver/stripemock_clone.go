package devserver

// Deep-clone helpers for the Stripe mock's in-memory resources.
//
// The serialize*/template-render paths must operate on data no concurrent
// writer can mutate. Read endpoints clone the stored struct while holding
// m.mu, then serialize/render the private copy after releasing the lock —
// the same clone-on-read discipline mailmock uses (see cloneMessage). The
// value copy (c := *x) is taken under the lock, so it's atomic with respect
// to writers; reference fields (maps, slices) are then deep-copied so the
// clone shares no mutable storage with the live state.
//
// Free functions (not methods) to match cloneMessage's form. The shared
// cloneStringMap helper lives in mailmock.go.

func cloneSession(s *stripeSession) *stripeSession {
	if s == nil {
		return nil
	}
	c := *s
	c.Metadata = cloneStringMap(s.Metadata)
	if s.LineItems != nil {
		c.LineItems = append([]stripeLineItem(nil), s.LineItems...)
	}
	return &c
}

func clonePaymentIntent(p *stripePaymentIntent) *stripePaymentIntent {
	if p == nil {
		return nil
	}
	c := *p
	c.Metadata = cloneStringMap(p.Metadata)
	return &c
}

func cloneAccount(a *stripeAccount) *stripeAccount {
	if a == nil {
		return nil
	}
	c := *a
	c.Metadata = cloneStringMap(a.Metadata)
	if a.CurrentlyDue != nil {
		c.CurrentlyDue = append([]string(nil), a.CurrentlyDue...)
	}
	return &c
}

func clonePayout(p *stripePayout) *stripePayout {
	if p == nil {
		return nil
	}
	c := *p
	c.Metadata = cloneStringMap(p.Metadata)
	return &c
}

func cloneRefund(r *stripeRefund) *stripeRefund {
	if r == nil {
		return nil
	}
	c := *r
	c.Metadata = cloneStringMap(r.Metadata)
	return &c
}

func cloneCharge(c *stripeCharge) *stripeCharge {
	if c == nil {
		return nil
	}
	cc := *c
	cc.Metadata = cloneStringMap(c.Metadata)
	return &cc
}
