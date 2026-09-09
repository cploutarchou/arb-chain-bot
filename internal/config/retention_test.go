package config

import (
	"testing"
	"time"
)

func TestLoadRetentionDefaults(t *testing.T) {
	c, err := LoadRetention()
	if err != nil {
		t.Fatalf("LoadRetention: %v", err)
	}
	want := defaultRetentionConfig()
	if c != want {
		t.Fatalf("LoadRetention() = %+v, want the documented defaults %+v", c, want)
	}
	// Spot-check the two policy numbers docs/data-flow.md §6 documents by
	// name, so a default drifting away from the doc fails loudly.
	if c.OpportunitiesQualified != 90*24*time.Hour {
		t.Fatalf("OpportunitiesQualified default = %s, want 90d", c.OpportunitiesQualified)
	}
	if c.OpportunitiesRejected != 14*24*time.Hour {
		t.Fatalf("OpportunitiesRejected default = %s, want 14d", c.OpportunitiesRejected)
	}
	// Financial evidence and the append-only tables have no field at all
	// — nothing to assert false for, which is the point: an operator
	// cannot configure them into eligibility because the knob does not
	// exist.
	if c.RecordingMetadata != 0 {
		t.Fatalf("RecordingMetadata default = %s, want 0 (disabled; operator opt-in)", c.RecordingMetadata)
	}
}

func TestLoadRetentionDurationOverrides(t *testing.T) {
	t.Setenv("RETENTION_OPPORTUNITIES_QUALIFIED", "45d")
	t.Setenv("RETENTION_EXCHANGE_HEALTH", "168h") // standard Go duration form
	t.Setenv("RETENTION_BATCH_SLEEP", "500ms")
	t.Setenv("RETENTION_RECORDING_METADATA", "7d")

	c, err := LoadRetention()
	if err != nil {
		t.Fatalf("LoadRetention: %v", err)
	}
	if c.OpportunitiesQualified != 45*24*time.Hour {
		t.Fatalf("OpportunitiesQualified = %s, want 45d", c.OpportunitiesQualified)
	}
	if c.ExchangeHealth != 168*time.Hour {
		t.Fatalf("ExchangeHealth = %s, want 168h", c.ExchangeHealth)
	}
	if c.BatchSleep != 500*time.Millisecond {
		t.Fatalf("BatchSleep = %s, want 500ms", c.BatchSleep)
	}
	if c.RecordingMetadata != 7*24*time.Hour {
		t.Fatalf("RecordingMetadata = %s, want 7d (opted in)", c.RecordingMetadata)
	}
	// Untouched fields keep their defaults.
	if c.OpportunitiesRejected != 14*24*time.Hour {
		t.Fatalf("OpportunitiesRejected = %s, want the untouched 14d default", c.OpportunitiesRejected)
	}
}

func TestLoadRetentionInvalidDuration(t *testing.T) {
	t.Setenv("RETENTION_SYSTEM_EVENTS", "not-a-duration")
	if _, err := LoadRetention(); err == nil {
		t.Fatal("want error for an unparsable duration")
	}
}

func TestLoadRetentionNegativeDurationRejected(t *testing.T) {
	t.Setenv("RETENTION_SESSIONS", "-5d")
	if _, err := LoadRetention(); err == nil {
		t.Fatal("want error for a negative retention window")
	}
}

func TestLoadRetentionRunAtUTC(t *testing.T) {
	t.Setenv("RETENTION_RUN_AT_UTC", "14:30")
	c, err := LoadRetention()
	if err != nil {
		t.Fatalf("LoadRetention: %v", err)
	}
	if c.RunAtHour != 14 || c.RunAtMinute != 30 {
		t.Fatalf("RunAt = %02d:%02d, want 14:30", c.RunAtHour, c.RunAtMinute)
	}

	for _, bad := range []string{"25:00", "10:70", "not-a-time", "10"} {
		t.Setenv("RETENTION_RUN_AT_UTC", bad)
		if _, err := LoadRetention(); err == nil {
			t.Fatalf("RETENTION_RUN_AT_UTC=%q: want error", bad)
		}
	}
}

func TestLoadRetentionBatchSize(t *testing.T) {
	t.Setenv("RETENTION_BATCH_SIZE", "50")
	c, err := LoadRetention()
	if err != nil {
		t.Fatalf("LoadRetention: %v", err)
	}
	if c.BatchSize != 50 {
		t.Fatalf("BatchSize = %d, want 50", c.BatchSize)
	}

	for _, bad := range []string{"0", "-1", "abc"} {
		t.Setenv("RETENTION_BATCH_SIZE", bad)
		if _, err := LoadRetention(); err == nil {
			t.Fatalf("RETENTION_BATCH_SIZE=%q: want error", bad)
		}
	}
}

func TestLoadRetentionDryRun(t *testing.T) {
	t.Setenv("RETENTION_DRY_RUN", "true")
	c, err := LoadRetention()
	if err != nil {
		t.Fatalf("LoadRetention: %v", err)
	}
	if !c.DryRun {
		t.Fatal("DryRun = false, want true")
	}

	t.Setenv("RETENTION_DRY_RUN", "not-a-bool")
	if _, err := LoadRetention(); err == nil {
		t.Fatal("want error for an unparsable RETENTION_DRY_RUN")
	}
}

func TestParseRetentionDuration(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"30d", 30 * 24 * time.Hour, false},
		{"0.5d", 12 * time.Hour, false},
		{"720h", 720 * time.Hour, false},
		{"5m", 5 * time.Minute, false},
		{"bogus", 0, true},
		{"d", 0, true},
	}
	for _, c := range cases {
		got, err := parseRetentionDuration(c.in)
		if (err != nil) != c.wantErr {
			t.Fatalf("parseRetentionDuration(%q) err = %v, wantErr %v", c.in, err, c.wantErr)
		}
		if err == nil && got != c.want {
			t.Fatalf("parseRetentionDuration(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}
