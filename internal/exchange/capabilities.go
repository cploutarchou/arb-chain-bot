package exchange

// Capability descriptors make venue differences explicit
// (docs/research/exchanges.md). Code branches on capabilities, never on
// exchange-name strings.

// BookInitModel is how a venue's L2 book is initialized.
type BookInitModel string

const (
	BookInitREST   BookInitModel = "REST_SNAPSHOT" // buffer deltas + REST splice (Binance)
	BookInitInBand BookInitModel = "IN_BAND"       // subscription delivers snapshot (OKX, Bybit, …)
)

// IntegrityModel is how gaps in the book stream are detectable.
type IntegrityModel string

const (
	IntegrityUpdateChain IntegrityModel = "UPDATE_ID_CHAIN" // per-stream U/u or seq/prevSeq chain
	IntegrityChecksum    IntegrityModel = "CHECKSUM"        // CRC over top-N levels
	IntegrityConnSeq     IntegrityModel = "CONN_SEQ"        // connection-global counter
	IntegrityResetOnly   IntegrityModel = "RESET_ONLY"      // silent gaps undetectable in-protocol
)

// FeeConvention is which asset the venue charges spot fees in.
type FeeConvention string

const (
	FeeInReceived FeeConvention = "RECEIVED" // buy→base, sell→quote (Binance, OKX, Bybit, Bitget)
	FeeInQuote    FeeConvention = "QUOTE"    // always quote (Coinbase)
	FeeInSpent    FeeConvention = "SPENT"    // buy→quote, sell→base (Kraken default)
)

// Capabilities describes one venue integration. RequiresRESTDriftCheck is
// set for integrity models that cannot detect silent gaps (RESET_ONLY) —
// the out-of-protocol cross-check becomes mandatory
// (docs/research/market-data.md §2).
type Capabilities struct {
	BookInit               BookInitModel
	Integrity              IntegrityModel
	RequiresRESTDriftCheck bool
	FeeConvention          FeeConvention
	HasSpotTestEnv         bool
	ForcedDisconnect       bool // scheduled reconnects needed (Binance 24h)
}
