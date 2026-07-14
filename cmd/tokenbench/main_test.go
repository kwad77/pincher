package main

import (
	"os"
	"testing"
)

func TestReadTranscript(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "responses-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"id\":1}\n{\"id\":2,\"source\":\"long body\"}\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	got, err := readTranscript(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if got.Calls != 2 || got.Tokens <= 0 {
		t.Fatalf("totals = %+v, want two calls and positive tokens", got)
	}
}

func TestReadTranscriptExchangeCountsInputAndOutput(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "exchange-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"input":"user prompt","output":"tool response"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	got, err := readTranscript(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if got.Calls != 1 || got.Input == 0 || got.Output == 0 || got.Tokens != got.Input+got.Output {
		t.Fatalf("exchange totals = %+v", got)
	}
}

func TestReadTranscriptRejectsMalformedJSON(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "bad-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("not-json\n")
	f.Close()
	if _, err := readTranscript(f.Name()); err == nil {
		t.Fatal("expected malformed JSON error")
	}
}
