package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// cliVersion is the CLI build version, overridden by ldflags at release time.
var cliVersion = "dev"

const doctorSchemaVersion = "mem.doctor/v1"

const deployComposeHint = "see deploy/compose/ for the recommended container deployment path"

type doctorCheck struct {
	Name    string `json:"name"`
	Code    string `json:"code"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

type doctorReport struct {
	SchemaVersion string          `json:"schema_version"`
	Checks        []doctorCheck   `json:"checks"`
	Summary       string          `json:"summary"`
	OK            bool            `json:"ok"`
}

type doctorHTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

var doctorHTTPClient doctorHTTPDoer = &http.Client{Timeout: 5 * time.Second}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose CLI connectivity and configuration",
		Long: `Read-only diagnostic that checks server reachability, credentials,
workspace selection, and CLI/server version compatibility. Performs no writes
and installs no dependencies.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := cmd.Flags().GetString("format")
			if err != nil {
				return err
			}
			if format != "text" && format != "json" {
				return fmt.Errorf("--format must be text or json, got %q", format)
			}

			report := runDoctorChecks()

			if format == "json" {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(report); err != nil {
					return err
				}
			} else {
				printDoctorReport(cmd, report)
			}
			return nil
		},
	}
}

func runDoctorChecks() doctorReport {
	cfg, cfgErr := resolveConfig("")

	checks := make([]doctorCheck, 0, 5)

	checks = append(checks, checkConfigLoaded(cfg, cfgErr))

	if cfgErr != nil || cfg == nil {
		checks = append(checks, doctorCheck{
			Name:    "server",
			Code:    "server_unreachable",
			Status:  "fail",
			Message: "server is not reachable",
			Hint:    deployComposeHint,
		})
		checks = append(checks, doctorCheck{
			Name:    "credentials",
			Code:    "no_credential",
			Status:  "fail",
			Message: "not logged in",
			Hint:    "run `mem auth login`; " + deployComposeHint,
		})
		checks = append(checks, doctorCheck{
			Name:    "workspace",
			Code:    "no_workspace",
			Status:  "warn",
			Message: "no workspace selected",
		})
	} else {
		checks = append(checks, checkServerReachable(cfg))
		checks = append(checks, checkCredentials(cfg))
		checks = append(checks, checkWorkspace(cfg))
		if isServerReachable(cfg) {
			checks = append(checks, checkVersionSkew(cfg))
		}
	}

	allOK := true
	for _, c := range checks {
		if c.Status == "fail" {
			allOK = false
			break
		}
	}

	summary := "all checks passed"
	if !allOK {
		failed := 0
		for _, c := range checks {
			if c.Status == "fail" {
				failed++
			}
		}
		summary = fmt.Sprintf("%d check(s) failed", failed)
	}

	return doctorReport{
		SchemaVersion: doctorSchemaVersion,
		Checks:        checks,
		Summary:       summary,
		OK:            allOK,
	}
}

func checkConfigLoaded(cfg *cliConfig, err error) doctorCheck {
	if err != nil {
		return doctorCheck{
			Name:    "config",
			Code:    "config_error",
			Status:  "fail",
			Message: "cannot read configuration",
		}
	}
	if cfg == nil {
		return doctorCheck{
			Name:    "config",
			Code:    "config_error",
			Status:  "fail",
			Message: "configuration is empty",
		}
	}
	return doctorCheck{
		Name:   "config",
		Code:   "config_ok",
		Status: "ok",
	}
}

func checkServerReachable(cfg *cliConfig) doctorCheck {
	if !isServerReachable(cfg) {
		return doctorCheck{
			Name:    "server",
			Code:    "server_unreachable",
			Status:  "fail",
			Message: "server is not reachable",
			Hint:    deployComposeHint,
		}
	}
	return doctorCheck{
		Name:   "server",
		Code:   "server_reachable",
		Status: "ok",
	}
}

func isServerReachable(cfg *cliConfig) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.Server+"/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := doctorHTTPClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func checkCredentials(cfg *cliConfig) doctorCheck {
	if cfg.Token == "" {
		return doctorCheck{
			Name:    "credentials",
			Code:    "no_credential",
			Status:  "fail",
			Message: "not logged in",
			Hint:    "run `mem auth login`; " + deployComposeHint,
		}
	}
	return doctorCheck{
		Name:   "credentials",
		Code:   "credential_present",
		Status: "ok",
	}
}

func checkWorkspace(cfg *cliConfig) doctorCheck {
	if strings.TrimSpace(cfg.Workspace) == "" {
		return doctorCheck{
			Name:    "workspace",
			Code:    "no_workspace",
			Status:  "warn",
			Message: "no workspace selected",
			Hint:    "use `--workspace` or set MEM_WORKSPACE",
		}
	}
	return doctorCheck{
		Name:   "workspace",
		Code:   "workspace_selected",
		Status: "ok",
	}
}

func checkVersionSkew(cfg *cliConfig) doctorCheck {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.Server+"/v1/version", nil)
	if err != nil {
		return doctorCheck{
			Name:    "version",
			Code:    "version_unknown",
			Status:  "warn",
			Message: "cannot check server version",
		}
	}
	resp, err := doctorHTTPClient.Do(req)
	if err != nil {
		return doctorCheck{
			Name:    "version",
			Code:    "version_unknown",
			Status:  "warn",
			Message: "cannot check server version",
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return doctorCheck{
			Name:    "version",
			Code:    "version_unknown",
			Status:  "warn",
			Message: "cannot check server version",
		}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return doctorCheck{
			Name:    "version",
			Code:    "version_unknown",
			Status:  "warn",
			Message: "cannot parse server version",
		}
	}
	var versionResp struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &versionResp); err != nil {
		return doctorCheck{
			Name:    "version",
			Code:    "version_unknown",
			Status:  "warn",
			Message: "cannot parse server version",
		}
	}

	cliV := cliVersion
	srvV := versionResp.Version
	if cliV == "dev" || srvV == "dev" {
		return doctorCheck{
			Name:    "version",
			Code:    "version_match",
			Status:  "ok",
			Message: "development build; version skew not checked",
		}
	}
	if cliV != srvV {
		return doctorCheck{
			Name:    "version",
			Code:    "version_skew",
			Status:  "warn",
			Message: fmt.Sprintf("CLI %s != server %s", cliV, srvV),
			Hint:    "update CLI or server to matching versions",
		}
	}
	return doctorCheck{
		Name:   "version",
		Code:   "version_match",
		Status: "ok",
	}
}

func printDoctorReport(cmd *cobra.Command, report doctorReport) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "mem doctor")
	fmt.Fprintln(out, "----------")
	for _, c := range report.Checks {
		icon := "ok"
		switch c.Status {
		case "fail":
			icon = "FAIL"
		case "warn":
			icon = "WARN"
		case "skip":
			icon = "SKIP"
		}
		line := fmt.Sprintf("  [%s] %s", icon, c.Name)
		if c.Message != "" {
			line += ": " + c.Message
		}
		fmt.Fprintln(out, line)
		if c.Hint != "" {
			fmt.Fprintf(out, "         hint: %s\n", c.Hint)
		}
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, report.Summary)
}
