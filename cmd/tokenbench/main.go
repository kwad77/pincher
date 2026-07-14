// tokenbench compares provider-visible response transcripts for the same
// replay workload. Each input is newline-delimited JSON (one model-visible
// tool response per line). The tool deliberately does not call a provider:
// capture identical runs with the provider's usage counters, then use this
// command to normalize and compare the response side of the exchange.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/kwad77/pincher/internal/db"
)

type totals struct {
	Calls  int
	Input  int
	Output int
	Tokens int
}

func readTranscript(path string) (totals, error) {
	f, err := os.Open(path)
	if err != nil {
		return totals{}, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	// Tool responses can contain large context bodies.
	s.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var out totals
	for s.Scan() {
		line := s.Bytes()
		if len(line) == 0 {
			continue
		}
		var exchange struct {
			Input  string `json:"input"`
			Output string `json:"output"`
		}
		if err := json.Unmarshal(line, &exchange); err != nil {
			return totals{}, fmt.Errorf("%s line %d: %w", path, out.Calls+1, err)
		}
		out.Calls++
		if exchange.Input != "" || exchange.Output != "" {
			out.Input += db.ApproxTokens(exchange.Input)
			out.Output += db.ApproxTokens(exchange.Output)
		} else {
			// Backward-compatible response-only JSONL format.
			out.Output += db.ApproxTokens(string(line))
		}
		out.Tokens = out.Input + out.Output
	}
	if err := s.Err(); err != nil {
		return totals{}, err
	}
	return out, nil
}

func report(label, path string, base totals) error {
	got, err := readTranscript(path)
	if err != nil {
		return err
	}
	pct := 0.0
	if base.Tokens > 0 {
		pct = float64(base.Tokens-got.Tokens) / float64(base.Tokens) * 100
	}
	fmt.Printf("%-8s calls=%d input=%d output=%d tokens=%d saved_vs_baseline=%d saved_pct=%.1f%%\n",
		label, got.Calls, got.Input, got.Output, got.Tokens, base.Tokens-got.Tokens, pct)
	return nil
}

func main() {
	baseline := flag.String("baseline", "", "newline-delimited baseline responses (required)")
	normal := flag.String("normal", "", "newline-delimited normal Pincher responses")
	save := flag.String("save", "", "newline-delimited token-saving Pincher responses")
	exact := flag.Bool("exact", false, "use exact cl100k-style BPE counts instead of chars/4")
	flag.Parse()
	if *exact {
		if err := os.Setenv("PINCHER_TOKEN_ACCOUNTING", "exact"); err != nil {
			fmt.Fprintln(os.Stderr, "tokenbench:", err)
			os.Exit(1)
		}
	}
	if *baseline == "" {
		fmt.Fprintln(os.Stderr, "tokenbench: -baseline is required")
		os.Exit(2)
	}
	base, err := readTranscript(*baseline)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tokenbench:", err)
		os.Exit(1)
	}
	fmt.Printf("baseline calls=%d input=%d output=%d tokens=%d\n", base.Calls, base.Input, base.Output, base.Tokens)
	if *normal != "" {
		if err := report("normal", *normal, base); err != nil {
			fmt.Fprintln(os.Stderr, "tokenbench:", err)
			os.Exit(1)
		}
	}
	if *save != "" {
		if err := report("save", *save, base); err != nil {
			fmt.Fprintln(os.Stderr, "tokenbench:", err)
			os.Exit(1)
		}
	}
}
