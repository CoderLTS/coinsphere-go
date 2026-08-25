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

func TestValidateAllowsExplicitLocalOverride(t *testing.T) {
	t.Setenv("COINSPHERE_ALLOW_INSECURE_SECRET", "1")
	cfg := &AppConfig{Auth: AuthConfig{SecretKey: DefaultInsecureSecret}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected the explicit local override: %v", err)
	}
}
