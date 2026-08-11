package config

import "testing"

func TestValidateRejectsInsecureSecretWithoutExplicitOverride(t *testing.T) {
	t.Setenv("COINSPHERE_ALLOW_INSECURE_SECRET", "")

	for _, secret := range []string{"", DefaultInsecureSecret} {
		cfg := &AppConfig{Auth: AuthConfig{SecretKey: secret}}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate() accepted insecure secret %q", secret)
		}
	}
}

func TestValidateAcceptsConfiguredSecret(t *testing.T) {
	t.Setenv("COINSPHERE_ALLOW_INSECURE_SECRET", "")
	cfg := &AppConfig{Auth: AuthConfig{SecretKey: "test-only-random-secret"}}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() returned an unexpected error: %v", err)
	}
}

func TestValidateAllowsInsecureSecretOnlyForExplicitLocalOverride(t *testing.T) {
	t.Setenv("COINSPHERE_ALLOW_INSECURE_SECRET", "1")
	cfg := &AppConfig{Auth: AuthConfig{SecretKey: DefaultInsecureSecret}}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected the explicit local override: %v", err)
	}
}

func TestMarketDataConfigBounds(t *testing.T) {
	cfg := defaultConfig()
	cfg.Auth.SecretKey = "test-only-random-secret"
	cfg.MarketData.ReconcileIntervalSeconds = 31
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted market-data reconciliation above 30 seconds")
	}

	cfg.MarketData.ReconcileIntervalSeconds = 30
	cfg.MarketData.BackfillPageSize = 301
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted a Binance page size above 300")
	}
}

func TestPrivateTradingDefaultsDisabled(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Trading.TestnetPrivateAPIEnabled || cfg.Trading.SpotLiveManualEnabled ||
		cfg.Trading.SpotLiveAutoEnabled || cfg.Trading.USDMLiveManualEnabled {
		t.Fatal("private trading must require explicit enablement")
	}
	cfg.Auth.SecretKey = "test-only-random-secret"
	cfg.Trading.SpotLiveAutoEnabled = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("Spot Live auto did not require the manual capability")
	}
	cfg.Trading.SpotLiveManualEnabled = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid Spot Live capability dependency returned %v", err)
	}
}
