package screener

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

func validSpreadRule() Rule {
	bps := decimal.NewFromInt(50)
	return Rule{
		ID: "r1", Name: "BTC cross-venue", Enabled: true, Kind: RuleKindSpread,
		MinSpreadBps: &bps, MinLiquidityQuote: d("1000"), MinLifetimeS: 10,
		BuyVenues: []Venue{VenueBinance}, SellVenues: []Venue{VenueOKX},
		Quotes: []string{"USDT"}, CooldownS: 60,
	}
}

// fixtureWebhookSecret is a deliberately low-entropy stand-in, not a secret.
const fixtureWebhookSecret = "wwwwwwwwwwwwwwww"

func TestRuleValidateSpreadRequiresMinSpreadBps(t *testing.T) {
	r := validSpreadRule()
	r.MinSpreadBps = nil
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestRuleValidateCarryRequiresMinCarryAPR(t *testing.T) {
	r := validSpreadRule()
	r.Kind = RuleKindCarry
	r.MinSpreadBps = nil
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid (missing min_carry_apr)", err)
	}
	apr := d("0.1")
	r.MinCarryAPR = &apr
	if err := r.Validate(); err != nil {
		t.Fatalf("valid carry rule rejected: %v", err)
	}
}

func TestRuleValidateUnknownKind(t *testing.T) {
	r := validSpreadRule()
	r.Kind = RuleKind("nonsense")
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestRuleValidateUnknownVenue(t *testing.T) {
	r := validSpreadRule()
	r.BuyVenues = []Venue{"dydx"}
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestRuleValidateAutoPaperRequiresSize(t *testing.T) {
	r := validSpreadRule()
	r.AutoPaper = true
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid (auto_paper without paper_size_quote)", err)
	}
	r.PaperSizeQuote = d("100")
	if err := r.Validate(); err != nil {
		t.Fatalf("valid auto_paper rule rejected: %v", err)
	}
}

func TestRuleValidateEmptyName(t *testing.T) {
	r := validSpreadRule()
	r.Name = ""
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestRuleValidateUnknownChannel(t *testing.T) {
	r := validSpreadRule()
	r.Channels = []string{"carrier-pigeon"}
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestRuleValidateDuplicateChannel(t *testing.T) {
	r := validSpreadRule()
	r.Channels = []string{"telegram", "telegram"}
	r.Telegram = true
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestRuleValidateEmailChannelRequiresValidAddress(t *testing.T) {
	r := validSpreadRule()
	r.Channels = []string{"email"}
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid (missing email_to)", err)
	}
	r.EmailTo = "not-an-address"
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid (invalid email_to)", err)
	}
	r.EmailTo = "trader@example.test"
	if err := r.Validate(); err != nil {
		t.Fatalf("valid email channel rejected: %v", err)
	}
}

func TestRuleValidateWebhookChannelRequiresURLAndSecret(t *testing.T) {
	r := validSpreadRule()
	r.Channels = []string{"webhook"}
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid (missing webhook_url/secret)", err)
	}
	r.WebhookURL = "not a url"
	r.WebhookSecret = fixtureWebhookSecret
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid (bad webhook_url)", err)
	}
	r.WebhookURL = "ftp://example.test/hook"
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid (non-http(s) scheme)", err)
	}
	r.WebhookURL = "https://hooks.example.test/screener"
	r.WebhookSecret = "short"
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid (webhook_secret too short)", err)
	}
	r.WebhookSecret = fixtureWebhookSecret
	if err := r.Validate(); err != nil {
		t.Fatalf("valid webhook channel rejected: %v", err)
	}
}

func TestRuleEffectiveChannelsFallsBackToTelegram(t *testing.T) {
	r := validSpreadRule()
	r.Telegram = true
	if got := r.EffectiveChannels(); len(got) != 1 || got[0] != "telegram" {
		t.Fatalf("EffectiveChannels() = %v, want [telegram]", got)
	}
	r.Channels = []string{"email"}
	r.EmailTo = "trader@example.test"
	if got := r.EffectiveChannels(); len(got) != 1 || got[0] != "email" {
		t.Fatalf("EffectiveChannels() with explicit Channels = %v, want [email] (Channels wins over legacy Telegram)", got)
	}
	r.Channels = nil
	r.Telegram = false
	if got := r.EffectiveChannels(); got != nil {
		t.Fatalf("EffectiveChannels() = %v, want nil", got)
	}
}

func TestRuleRedactStripsWebhookSecret(t *testing.T) {
	r := validSpreadRule()
	r.Channels = []string{"webhook"}
	r.WebhookURL = "https://hooks.example.test/screener"
	r.WebhookSecret = fixtureWebhookSecret
	red := r.Redact()
	if red.WebhookSecret != "" {
		t.Fatalf("Redact() left WebhookSecret = %q", red.WebhookSecret)
	}
	if r.WebhookSecret == "" {
		t.Fatal("Redact() must not mutate the receiver's copy in place for the original rule")
	}
}
