package config

import (
	"errors"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

func loadGitHub(cfg *Config) error {
	if raw := os.Getenv("MEM_GITHUB_BOOTSTRAP"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return errors.New("MEM_GITHUB_BOOTSTRAP must be true or false")
		}
		cfg.GitHubBootstrap = value
	}
	cfg.PublicURL = strings.TrimRight(strings.TrimSpace(os.Getenv("MEM_PUBLIC_URL")), "/")
	cfg.GitHubClientID = strings.TrimSpace(os.Getenv("MEM_GITHUB_CLIENT_ID"))
	cfg.GitHubClientSecret = os.Getenv("MEM_GITHUB_CLIENT_SECRET")
	raw := strings.TrimSpace(os.Getenv("MEM_GITHUB_ALLOWED_USER_IDS"))
	if cfg.GitHubClientID == "" && cfg.GitHubClientSecret == "" && raw == "" {
		if cfg.GitHubBootstrap {
			return errors.New("GitHub bootstrap requires configured GitHub OAuth")
		}
		return nil
	}
	if cfg.PublicURL == "" || cfg.GitHubClientID == "" || strings.TrimSpace(cfg.GitHubClientSecret) == "" || raw == "" {
		return errors.New("GitHub OAuth requires MEM_PUBLIC_URL, MEM_GITHUB_CLIENT_ID, MEM_GITHUB_CLIENT_SECRET and MEM_GITHUB_ALLOWED_USER_IDS")
	}
	u, err := url.Parse(cfg.PublicURL)
	if cfg.GitHubBootstrap && cfg.RegistrationMode != "disabled" {
		return errors.New("GitHub bootstrap requires MEM_REGISTRATION_MODE=disabled")
	}
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("MEM_PUBLIC_URL must be an exact origin without credentials, path, query or fragment")
	}
	if u.Scheme != "https" && !(cfg.RuntimeProfile == "development" && u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")) {
		return errors.New("MEM_PUBLIC_URL must use HTTPS (development may use localhost HTTP)")
	}
	pattern := regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
	seen := map[string]bool{}
	for _, value := range strings.Split(raw, ",") {
		id := strings.TrimSpace(value)
		if !pattern.MatchString(id) || seen[id] {
			return errors.New("MEM_GITHUB_ALLOWED_USER_IDS must contain unique numeric GitHub user IDs")
		}
		seen[id] = true
		cfg.GitHubAllowedUserIDs = append(cfg.GitHubAllowedUserIDs, id)
	}
	return nil
}
