# Token replay benchmark

`cmd/tokenbench` compares identical navigation workloads captured as
newline-delimited JSON. A line may be a raw response (backward-compatible) or
an exchange with `{"input":"...","output":"..."}`. Use exact BPE mode for
the most faithful local estimate:

```bash
go run ./cmd/tokenbench \
  -baseline native-read-grep.jsonl \
  -normal pincher-normal.jsonl \
  -save pincher-save.jsonl \
  -exact
```

Example output:

```text
baseline calls=12 input=12000 output=6420 tokens=18420
normal   calls=8 input=5000 output=2340 tokens=7340 saved_vs_baseline=11080 saved_pct=60.2%
save     calls=6 input=2800 output=1410 tokens=4210 saved_vs_baseline=14210 saved_pct=77.1%
```

For provider-billing accuracy, capture each run with the provider's reported
input-token usage and record it alongside the transcript. `tokenbench` measures
the model-visible payload; it cannot infer provider-side prompt caching,
system prompts, or hidden tool-schema tokens. Run the same task seed and
working tree for all three modes, and report both provider counters and this
payload-level breakdown.
