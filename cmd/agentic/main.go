package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"

	_ "modernc.org/sqlite"

	"github.com/yasinnerten/agentic-golang/runtime"
)

func main() {
	var (
		dbPath   = flag.String("db", "agentic.db", "sqlite database path")
		agent    = flag.String("agent", "default", "agent name")
		input    = flag.String("input", "", "task input")
		runOnce  = flag.Bool("run-once", false, "run a single pending task")
		register = flag.Bool("register-default", true, "ensure default agent exists")
	)
	flag.Parse()

	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	r := runtime.New(db, runtime.Providers{
		runtime.NewStaticProvider("primary", true, ""),
		runtime.NewStaticProvider("secondary", false, "fallback response"),
	})
	ctx := context.Background()
	if err := r.InitSchema(ctx); err != nil {
		log.Fatal(err)
	}

	if *register {
		err = r.RegisterAgent(ctx, runtime.AgentSpec{
			Name:                *agent,
			SystemPrompt:        "general assistant",
			RetryPolicy:         runtime.RetryPolicy{MaxAttempts: 2, BackoffMillis: 1},
			SemanticThreshold:   0.70,
			PromptPrefix:        "You are a helpful agent.",
			ObservabilityPolicy: "default",
		})
		if err != nil {
			log.Fatal(err)
		}
	}

	if *input != "" {
		id, err := r.EnqueueTask(ctx, *agent, *input)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(os.Stdout, "enqueued task %d\n", id)
	}

	if *runOnce {
		res, err := r.RunNextTask(ctx)
		if err != nil {
			log.Fatal(err)
		}
		if res == nil {
			fmt.Fprintln(os.Stdout, "no pending tasks")
			return
		}
		fmt.Fprintf(os.Stdout, "task=%d status=%s provider=%s cache=%s latency_ms=%d cost=%.6f\n",
			res.TaskID, res.Status, res.Provider, res.CacheTier, res.LatencyMs, res.CostUSD)
		if res.Output != "" {
			fmt.Fprintf(os.Stdout, "output=%s\n", res.Output)
		}
		if res.Error != "" {
			fmt.Fprintf(os.Stdout, "error=%s\n", res.Error)
		}
	}
}
