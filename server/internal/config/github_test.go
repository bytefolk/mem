package config

import "testing"

func TestGitHubConfigurationFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, url, ids, registration, bootstrap string
		want                                    bool
	}{
		{"personal", "https://mem.example", "47820304", "disabled", "true", true},
		{"missing-id", "https://mem.example", "", "disabled", "true", false},
		{"duplicate-id", "https://mem.example", "47820304,47820304", "disabled", "false", false},
		{"username-not-subject", "https://mem.example", "PeterGuy326", "disabled", "false", false},
		{"insecure-url", "http://mem.example", "47820304", "disabled", "false", false},
		{"url-path", "https://mem.example/path", "47820304", "disabled", "false", false},
		{"url-credentials", "https://secret@mem.example", "47820304", "disabled", "false", false},
		{"public-password-bootstrap", "https://mem.example", "47820304", "first_user", "true", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MEM_PUBLIC_URL", tc.url)
			t.Setenv("MEM_GITHUB_CLIENT_ID", "client")
			t.Setenv("MEM_GITHUB_CLIENT_SECRET", "secret")
			t.Setenv("MEM_GITHUB_ALLOWED_USER_IDS", tc.ids)
			t.Setenv("MEM_GITHUB_BOOTSTRAP", tc.bootstrap)
			cfg := &Config{RuntimeProfile: "production", RegistrationMode: tc.registration}
			err := loadGitHub(cfg)
			if (err == nil) != tc.want {
				t.Fatalf("configuration result: %v", err)
			}
		})
	}
}
