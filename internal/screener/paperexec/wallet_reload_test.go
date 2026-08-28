package paperexec

import (
	"context"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// Editing paper.balances must reach the executor: before T-096 the
// wallets latched at first load, so an operator adding an asset saw
// nothing change until the process was restarted.
func TestReloadWalletsPicksUpNewlySeededAssets(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	h.balance(screener.VenueBinance, "USDT", "1000")

	x := h.x
	if err := x.ensureWallets(ctx); err != nil {
		t.Fatal(err)
	}
	if w := x.wallet(screener.VenueBinance); w == nil {
		t.Fatal("no wallet after the first load")
	}

	// Add an asset the way a settings apply does, then reload.
	h.balance(screener.VenueBinance, "ETH", "5")
	x.ReloadWallets()
	if err := x.ensureWallets(ctx); err != nil {
		t.Fatal(err)
	}
	w := x.wallet(screener.VenueBinance)
	if avail, _ := w.Balance("ETH"); avail.String() != "5" {
		t.Fatalf("ETH balance after reload = %s, want 5", avail)
	}
	if avail, _ := w.Balance("USDT"); avail.String() != "1000" {
		t.Fatalf("USDT balance = %s, want the ledger value 1000", avail)
	}
}
