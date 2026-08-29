package entitlements

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestNoPackageAdvertisesUnbuiltDex is the T-102 regression: the Desk and
// Institution documents shipped DexEnabled: true and the "dex" screener
// tier with no DEX code in the tree. Nothing may advertise it again
// before DexImplemented flips.
func TestNoPackageAdvertisesUnbuiltDex(t *testing.T) {
	if DexImplemented {
		t.Skip("DEX shipped; this guard is superseded by the tree cross-check")
	}
	for _, code := range Codes {
		p, ok := Package(code)
		if !ok {
			t.Fatalf("package %q missing", code)
		}
		if p.Venues.DexEnabled {
			t.Errorf("package %q sets venues.dex_enabled with no DEX venue in the build (T-102)", code)
		}
		if Has(p.Venues.ScreenerTiers, dexTier) {
			t.Errorf("package %q advertises the %q screener tier with no DEX venue in the build (T-102)", code, dexTier)
		}
	}
}

// TestValidateRejectsUnbuiltDex proves the guard is enforcement, not a
// convention: a document that advertises DEX is refused wherever it comes
// from, with the sentinel that distinguishes "not built" from "not a word".
func TestValidateRejectsUnbuiltDex(t *testing.T) {
	if DexImplemented {
		t.Skip("DEX shipped; the capability is no longer refused")
	}
	base, ok := Package(PackageDesk)
	if !ok {
		t.Fatal("Desk package missing")
	}

	tiers := base.Clone()
	tiers.Venues.ScreenerTiers = []string{"tier1", "tier2", dexTier}
	if err := Validate(tiers); !errors.Is(err, ErrUnimplemented) {
		t.Errorf("screener_tiers with %q: got %v, want ErrUnimplemented", dexTier, err)
	} else if !errors.Is(err, ErrInvalid) {
		t.Errorf("ErrUnimplemented must also satisfy errors.Is(err, ErrInvalid); got %v", err)
	}

	flag := base.Clone()
	flag.Venues.DexEnabled = true
	if err := Validate(flag); !errors.Is(err, ErrUnimplemented) {
		t.Errorf("dex_enabled true: got %v, want ErrUnimplemented", err)
	}

	// An unknown tier is still an enum failure, not an unimplemented one:
	// the two answers mean different things to whoever wrote the document.
	unknown := base.Clone()
	unknown.Venues.ScreenerTiers = []string{"tier1", "tier9"}
	err := Validate(unknown)
	if !errors.Is(err, ErrInvalid) || errors.Is(err, ErrUnimplemented) {
		t.Errorf("unknown tier: got %v, want a plain enum ErrInvalid", err)
	}
}

// TestOverrideCannotEnableUnbuiltDex closes the widening path: an
// organisation override is the one place a capability could be switched
// on per tenant without touching packages.go. Resolve must refuse it and
// fall back to the bare package rather than serve a document promising a
// tier the build cannot screen.
func TestOverrideCannotEnableUnbuiltDex(t *testing.T) {
	if DexImplemented {
		t.Skip("DEX shipped; the override is no longer a widening")
	}
	base, ok := Package(PackageDesk)
	if !ok {
		t.Fatal("Desk package missing")
	}
	override := []byte(`{"venues":{"dex_enabled":true,"screener_tiers":["tier1","tier2","dex"]}}`)

	if _, err := Merge(base, override); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("Merge: got %v, want ErrUnimplemented", err)
	}

	src := NewMapSource()
	src.Set(Input{OrgID: 7, PackageCode: PackageDesk, Override: override, SubStatus: "active"})
	var rejected error
	r := NewResolver(src)
	r.OnError = func(_ int64, err error) { rejected = err }

	doc, err := r.For(context.Background(), 7)
	if err != nil {
		t.Fatalf("Resolve after bad override: %v", err)
	}
	if !errors.Is(rejected, ErrUnimplemented) {
		t.Errorf("override rejection not surfaced: got %v", rejected)
	}
	if doc.Venues.DexEnabled || Has(doc.Venues.ScreenerTiers, dexTier) {
		t.Errorf("fallback document still advertises DEX: %+v", doc.Venues)
	}
}

// TestDexTierStaysInTheSchemaVocabulary pins the deliberate asymmetry:
// T-102 removed the capability from the packages, NOT the word from the
// schema. Stored documents keep a stable vocabulary across the flip, and
// T-116 re-enables the tier by changing one constant rather than by
// bumping SchemaVersion and migrating every stored override.
func TestDexTierStaysInTheSchemaVocabulary(t *testing.T) {
	if !Has(enumTiers, dexTier) {
		t.Errorf("enumTiers no longer accepts %q; T-116 would need a schema migration", dexTier)
	}
	var schema struct {
		Properties struct {
			Venues struct {
				Properties struct {
					ScreenerTiers struct {
						Items struct {
							Enum []string `json:"enum"`
						} `json:"items"`
					} `json:"screener_tiers"`
				} `json:"properties"`
			} `json:"venues"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(SchemaJSON, &schema); err != nil {
		t.Fatalf("schema.v1.json: %v", err)
	}
	got := schema.Properties.Venues.Properties.ScreenerTiers.Items.Enum
	if !Has(got, dexTier) {
		t.Errorf("schema.v1.json screener_tiers enum %v lost %q", got, dexTier)
	}
	if strings.Join(got, ",") != strings.Join(enumTiers, ",") {
		t.Errorf("enumTiers %v and schema enum %v have drifted", enumTiers, got)
	}
}
