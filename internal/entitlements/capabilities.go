package entitlements

import "fmt"

// DexImplemented reports whether this build ships DEX venue coverage:
// quote sources, canonical token lists, a gas-aware cost model and at
// least one registered DEX venue (T-076, decomposed into T-110..T-116;
// design of record docs/design/dex-arbitrage.md).
//
// It is false, and stays false until T-116 lands. T-102 found the Desk
// and Institution packages shipping DexEnabled: true and the screener
// tier "dex" with no DEX code anywhere in internal/ — the two most
// expensive packages advertising a venue tier with nothing behind it.
// The operator's decision (2026-08-29) was to switch the capability off
// until it is built rather than hold the packages as a launch blocker.
//
// Flipping this to true is the last step of T-116, not a way to make a
// package document look richer: TestDexCapabilityMatchesTree fails if it
// is true while no DEX venue is registered in the tree.
const DexImplemented = false

// ErrUnimplemented is the T-102 guard: a document — package, override or
// merge result — advertising a capability this build does not implement.
// It is a distinct sentinel from a plain schema failure because the two
// mean different things to an operator. A rejected *word* is a typo; a
// rejected *capability* is the platform refusing to sell something that
// does not exist. The generalised rule is recorded in
// docs/design/crypto-arb-platform-command.md §"Parity review": an
// advertised package capability must resolve to something in the tree.
var ErrUnimplemented = fmt.Errorf("%w: capability not implemented in this build", ErrInvalid)

// dexTier is the screener tier name for DEX venue coverage. It stays in
// enumTiers and in schema.v1.json so stored documents and overrides keep
// a stable vocabulary across the flip — the schema says which words are
// spellable, this file says which ones this build can honour.
const dexTier = "dex"

// checkImplemented rejects capabilities the build cannot deliver. It runs
// after the enum checks, so an unknown tier is reported as an enum
// failure and a known-but-unbuilt tier as an unimplemented one.
func checkImplemented(e Entitlements) error {
	if DexImplemented {
		return nil
	}
	if e.Venues.DexEnabled {
		return fmt.Errorf("%w: venues.dex_enabled is true but no DEX venue ships in this build (T-076)", ErrUnimplemented)
	}
	if Has(e.Venues.ScreenerTiers, dexTier) {
		return fmt.Errorf("%w: venues.screener_tiers includes %q but no DEX venue ships in this build (T-076)", ErrUnimplemented, dexTier)
	}
	return nil
}
