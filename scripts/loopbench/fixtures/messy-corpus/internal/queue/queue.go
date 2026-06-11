// Package queue hands jobs to the Python fulfillment workers. Jobs are JSON
// lines keyed by a job-type string; the worker side resolves the type to a
// handler through its own registry (workers/lib/registry.py).
package queue

import (
	"encoding/json"
	"fmt"
	"os"
)

// Publish appends one job to the shared queue file.
func Publish(jobType string, payload map[string]any) error {
	payload["job_type"] = jobType
	line, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	f, err := os.OpenFile("/tmp/orderflow-queue.jsonl", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s\n", line)
	return err
}
