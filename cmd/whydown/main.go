package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"whydown/internal/ai"
	"whydown/internal/docker"
	"whydown/internal/domain"
	evaluation "whydown/internal/eval"
	"whydown/internal/mcp"
	"whydown/internal/redact"
)

const usage = `Whydown explains why Docker containers fail.

Usage: whydown <command> [options] [container]

Commands:
  list       List Docker containers
  inspect    Print sanitized evidence for a container
  diagnose   Explain a container failure
  watch      Monitor Docker failures and print reports
  eval       Score diagnoses against recorded failure fixtures
  mcp        Serve read-only assistant tools over stdin/stdout

Options follow the command and precede the container:
  --provider groq|google|offline    Default: WHYDOWN_PROVIDER or groq
  --model NAME                      Override the provider model
  --json                            Print reports as JSON
  --cases PATH                      Evaluation cases (default testdata/failures.json)

Credentials: GROQ_API_KEY or GOOGLE_API_KEY (GEMINI_API_KEY also accepted).
Docker connection: DOCKER_HOST, DOCKER_TLS_VERIFY, DOCKER_CERT_PATH.
Offline is an explicit heuristic baseline. AI errors never fall back silently.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "whydown:", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, in io.Reader, out, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		_, err := io.WriteString(out, usage)
		return err
	}
	command := args[0]
	if !strings.Contains("|list|inspect|diagnose|watch|eval|mcp|", "|"+command+"|") {
		return fmt.Errorf("unknown command %q; run whydown help", command)
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	defaultProvider := os.Getenv("WHYDOWN_PROVIDER")
	if defaultProvider == "" {
		defaultProvider = "groq"
	}
	provider := fs.String("provider", defaultProvider, "groq, google, or offline")
	model := fs.String("model", os.Getenv("WHYDOWN_MODEL"), "AI model")
	asJSON := fs.Bool("json", false, "JSON output")
	casesPath := fs.String("cases", "testdata/failures.json", "evaluation case file")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	needsID := command == "inspect" || command == "diagnose"
	if (needsID && fs.NArg() != 1) || (!needsID && fs.NArg() != 0) {
		return fmt.Errorf("%s requires %s", command, map[bool]string{true: "one container name or ID", false: "no positional arguments"}[needsID])
	}
	var diagnose evaluation.Diagnose
	if command == "diagnose" || command == "watch" || command == "eval" || command == "mcp" {
		if *provider == "offline" {
			diagnose = func(_ context.Context, e domain.Evidence) (domain.Report, error) { return ai.Offline(e), nil }
		} else {
			key := os.Getenv("GROQ_API_KEY")
			if *provider == "google" || *provider == "gemini" {
				key = os.Getenv("GOOGLE_API_KEY")
				if key == "" {
					key = os.Getenv("GEMINI_API_KEY")
				}
			}
			client, err := ai.New(ai.Config{Provider: *provider, APIKey: key, Model: *model})
			if err != nil {
				return err
			}
			diagnose = client.Diagnose
		}
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if command == "eval" {
		cases, err := evaluation.Load(*casesPath)
		if err != nil {
			return err
		}
		score := evaluation.Run(ctx, *provider+" recorded-fixtures", cases, diagnose)
		if err = enc.Encode(score); err != nil {
			return err
		}
		if score.Passed != score.Total {
			return fmt.Errorf("evaluation failed: %d of %d cases passed", score.Passed, score.Total)
		}
		return nil
	}
	client, err := docker.New()
	if err != nil {
		return err
	}
	defer client.Close()
	safe := func(e domain.Evidence) domain.Evidence {
		return redact.Evidence(e, os.Getenv("GROQ_API_KEY"), os.Getenv("GOOGLE_API_KEY"), os.Getenv("GEMINI_API_KEY"))
	}
	switch command {
	case "mcp":
		return (mcp.Server{Docker: client, Diagnose: diagnose, Sanitize: safe}).Serve(ctx, in, out)
	case "list":
		containers, err := client.List(ctx)
		if err != nil {
			return err
		}
		return enc.Encode(containers)
	case "inspect":
		e, err := client.Collect(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		return enc.Encode(safe(e))
	case "diagnose":
		e, err := client.Collect(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		r, err := diagnose(ctx, e)
		if err != nil {
			return err
		}
		return printReport(out, *asJSON, r)
	case "watch":
		for {
			err := client.Watch(ctx, func(e domain.Evidence) error {
				r, err := diagnose(ctx, e)
				if err != nil {
					fmt.Fprintln(stderr, "Diagnosis failed:", err)
					return nil
				}
				return printReport(out, *asJSON, r)
			})
			if ctx.Err() != nil {
				return ctx.Err()
			}
			fmt.Fprintln(stderr, "Monitor disconnected; retrying in 5 seconds:", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}
	}
	return nil
}
func printReport(out io.Writer, asJSON bool, r domain.Report) error {
	if asJSON {
		return json.NewEncoder(out).Encode(r)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s\n%s\n\nWhy: %s\nConfidence: %s | Provider: %s\n", r.Container, r.Summary, r.Cause, r.Confidence, r.Provider)
	for _, fix := range r.Fixes {
		fmt.Fprintf(&b, "  - %s\n", fix)
	}
	for _, fact := range r.Evidence {
		fmt.Fprintf(&b, "Evidence: %s\n", fact)
	}
	_, err := io.WriteString(out, b.String())
	return err
}
