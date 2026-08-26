package scanner

import (
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
)

// Full pipeline per dirty market: views → pre-gates → capacity → size
// search → opportunity build → risk evaluation → emit. The §73
// "triangle recalculation" benchmark.
func BenchmarkEvaluateMarket(b *testing.B) {
	s, _ := harness(b)
	// Drain events so emission cost is measured, not drop accounting.
	done := make(chan struct{})
	go func() {
		for range s.Out {
		}
		close(done)
	}()
	id := exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.EvaluateMarket(id)
	}
	b.StopTimer()
	close(s.Out)
	<-done
}
