package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/PeterGuy326/mem/server/internal/apiclient"
	"github.com/spf13/cobra"
)

const maxHandoffInputBytes = 512 << 10

func newCheckpointCmd() *cobra.Command {
	var (
		inputPath      string
		idempotencyKey string
	)
	cmd := &cobra.Command{
		Use:   "checkpoint",
		Short: "Save a portable Agent task checkpoint",
		Long: `Persist one strict mem.handoff v1 document so another Agent or
computer can continue the same task.

Examples:
  mem checkpoint --input handoff.json --idempotency-key task-42-checkpoint-1
  cat handoff.json | mem checkpoint --input - --idempotency-key task-42-checkpoint-1`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(inputPath) == "" {
				return errors.New("--input must not be empty")
			}
			idempotencyKey = strings.TrimSpace(idempotencyKey)
			if idempotencyKey == "" {
				return errors.New("--idempotency-key must not be empty")
			}
			format, err := rememberOutputFormat(cmd)
			if err != nil {
				return err
			}

			document, err := readHandoffInput(cmd, inputPath)
			if err != nil {
				return err
			}
			if err := document.Validate(); err != nil {
				return fmt.Errorf("invalid handoff input: %w", err)
			}

			cfg, err := resolveConfig("")
			if err != nil {
				return err
			}
			if cfg.Token == "" {
				return newCliError(3, "not logged in", "run `mem auth login` first")
			}
			raw, err := newHTTPClient(cfg).api.Checkpoint(
				commandContext(cmd),
				document.TaskKey,
				document,
				idempotencyKey,
			)
			if err != nil {
				return fromAPIError(err)
			}
			if format == "json" {
				return writeHandoffJSON(cmd.OutOrStdout(), raw)
			}
			return printCheckpointText(cmd.OutOrStdout(), raw)
		},
	}
	cmd.Flags().StringVar(&inputPath, "input", "", "handoff v1 JSON file (- for stdin)")
	cmd.Flags().StringVar(
		&idempotencyKey,
		"idempotency-key",
		"",
		"stable retry key, unique within the workspace",
	)
	_ = cmd.MarkFlagRequired("input")
	_ = cmd.MarkFlagRequired("idempotency-key")
	return cmd
}

func newResumeCmd() *cobra.Command {
	var (
		checkpointID string
		scope        string
		focus        string
		limit        int
		maxChars     int
	)
	cmd := &cobra.Command{
		Use:   "resume <task-key>",
		Short: "Load a portable Agent task checkpoint",
		Long: `Resolve an explicit checkpoint, or the current task head, together
with its verified references and a bounded related context pack.

Examples:
  mem resume task-42
  mem resume task-42 --scope /Projects/mem --focus "remaining migration work"
  mem resume task-42 --checkpoint-id 4ca7... --format json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := rememberOutputFormat(cmd)
			if err != nil {
				return err
			}
			taskKey := args[0]
			request := apiclient.ResumeRequest{
				CheckpointID: strings.TrimSpace(checkpointID),
				Scope:        strings.TrimSpace(scope),
				Focus:        strings.TrimSpace(focus),
				Limit:        limit,
				MaxChars:     maxChars,
			}

			cfg, err := resolveConfig("")
			if err != nil {
				return err
			}
			if cfg.Token == "" {
				return newCliError(3, "not logged in", "run `mem auth login` first")
			}
			raw, err := newHTTPClient(cfg).api.Resume(
				commandContext(cmd),
				taskKey,
				request,
			)
			if err != nil {
				return fromAPIError(err)
			}
			if format == "json" {
				return writeHandoffJSON(cmd.OutOrStdout(), raw)
			}
			return printResumeText(cmd.OutOrStdout(), raw)
		},
	}
	cmd.Flags().StringVar(
		&checkpointID,
		"checkpoint-id",
		"",
		"resume an explicit immutable checkpoint UUID (default current head)",
	)
	cmd.Flags().StringVar(&scope, "scope", "", "narrow evidence to an absolute virtual path")
	cmd.Flags().StringVar(&focus, "focus", "", "semantic focus for related context")
	cmd.Flags().IntVar(&limit, "limit", 0, "max related evidence items")
	cmd.Flags().IntVar(&maxChars, "max-chars", 0, "related context character budget")
	return cmd
}

func readHandoffInput(cmd *cobra.Command, inputPath string) (apiclient.HandoffV1, error) {
	var (
		reader io.Reader
		close  func() error
	)
	if inputPath == "-" {
		reader = cmd.InOrStdin()
	} else {
		file, err := os.Open(inputPath)
		if err != nil {
			return apiclient.HandoffV1{}, fmt.Errorf("open handoff input: %w", err)
		}
		reader = file
		close = file.Close
	}
	if close != nil {
		defer close()
	}

	data, err := io.ReadAll(io.LimitReader(reader, maxHandoffInputBytes+1))
	if err != nil {
		return apiclient.HandoffV1{}, fmt.Errorf("read handoff input: %w", err)
	}
	if len(data) > maxHandoffInputBytes {
		return apiclient.HandoffV1{}, fmt.Errorf(
			"handoff input exceeds %d bytes",
			maxHandoffInputBytes,
		)
	}

	var document apiclient.HandoffV1
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return apiclient.HandoffV1{}, fmt.Errorf("decode handoff input: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return apiclient.HandoffV1{}, errors.New(
			"decode handoff input: expected exactly one JSON document",
		)
	} else if !errors.Is(err, io.EOF) {
		return apiclient.HandoffV1{}, fmt.Errorf("decode handoff input: %w", err)
	}
	return document, nil
}

func commandContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}

func writeHandoffJSON(w io.Writer, raw json.RawMessage) error {
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, raw, "", "  "); err != nil {
		return fmt.Errorf("decode server response: %w", err)
	}
	formatted.WriteByte('\n')
	_, err := w.Write(formatted.Bytes())
	return err
}

func printCheckpointText(w io.Writer, raw json.RawMessage) error {
	var response struct {
		Checkpoint struct {
			ID       string `json:"id"`
			TaskKey  string `json:"task_key"`
			Sequence int64  `json:"sequence"`
			Scope    string `json:"scope_path"`
		} `json:"checkpoint"`
		Replayed bool `json:"replayed"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("decode server response: %w", err)
	}
	if response.Checkpoint.ID == "" {
		return errors.New("decode server response: checkpoint.id is missing")
	}
	fmt.Fprintf(w, "checkpoint: %s\n", response.Checkpoint.ID)
	if response.Checkpoint.TaskKey != "" {
		fmt.Fprintf(w, "task: %s\n", response.Checkpoint.TaskKey)
	}
	if response.Checkpoint.Sequence > 0 {
		fmt.Fprintf(w, "sequence: %d\n", response.Checkpoint.Sequence)
	}
	if response.Checkpoint.Scope != "" {
		fmt.Fprintf(w, "scope: %s\n", response.Checkpoint.Scope)
	}
	fmt.Fprintf(w, "replayed: %t\n", response.Replayed)
	return nil
}

func printResumeText(w io.Writer, raw json.RawMessage) error {
	var response struct {
		Task struct {
			TaskKey string `json:"task_key"`
		} `json:"task"`
		Checkpoint struct {
			ID       string `json:"id"`
			Sequence int64  `json:"sequence"`
			Handoff  struct {
				State struct {
					Status   string `json:"status"`
					Goal     string `json:"goal"`
					Progress struct {
						Summary string `json:"summary"`
					} `json:"progress"`
				} `json:"state"`
			} `json:"handoff"`
		} `json:"checkpoint"`
		Resolved []json.RawMessage `json:"resolved"`
		Missing  []json.RawMessage `json:"missing"`
		Complete bool              `json:"complete"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("decode server response: %w", err)
	}
	if response.Checkpoint.ID == "" {
		return errors.New("decode server response: checkpoint.id is missing")
	}
	if response.Task.TaskKey != "" {
		fmt.Fprintf(w, "task: %s\n", response.Task.TaskKey)
	}
	fmt.Fprintf(w, "checkpoint: %s\n", response.Checkpoint.ID)
	if response.Checkpoint.Sequence > 0 {
		fmt.Fprintf(w, "sequence: %d\n", response.Checkpoint.Sequence)
	}
	if response.Checkpoint.Handoff.State.Status != "" {
		fmt.Fprintf(w, "status: %s\n", response.Checkpoint.Handoff.State.Status)
	}
	if response.Checkpoint.Handoff.State.Goal != "" {
		fmt.Fprintf(w, "goal: %s\n", response.Checkpoint.Handoff.State.Goal)
	}
	if response.Checkpoint.Handoff.State.Progress.Summary != "" {
		fmt.Fprintf(
			w,
			"progress: %s\n",
			response.Checkpoint.Handoff.State.Progress.Summary,
		)
	}
	fmt.Fprintf(w, "references: %d resolved, %d missing\n",
		len(response.Resolved), len(response.Missing))
	fmt.Fprintf(w, "complete: %t\n", response.Complete)
	return nil
}
