package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"evok8s-scheduler/pkg/evaluator"
)

type options struct {
	namespace  string
	deployment string
	interval   time.Duration
	timeout    time.Duration
}

func main() {
	opts := parseFlags()
	if err := run(opts); err != nil {
		log.Fatal(err)
	}
}

func parseFlags() options {
	opts := options{}
	flag.StringVar(&opts.namespace, "namespace", "evo-sched", "Scheduler deployment namespace")
	flag.StringVar(&opts.deployment, "deployment", "evok8s-scheduler", "Scheduler deployment name")
	flag.DurationVar(&opts.interval, "interval", 2*time.Second, "Desired scheduler interval")
	flag.DurationVar(&opts.timeout, "timeout", 2*time.Minute, "Update and rollout timeout")
	flag.Parse()
	return opts
}

func validateOptions(opts options) error {
	if strings.TrimSpace(opts.namespace) == "" {
		return fmt.Errorf("--namespace is required")
	}
	if strings.TrimSpace(opts.deployment) == "" {
		return fmt.Errorf("--deployment is required")
	}
	if opts.interval <= 0 {
		return fmt.Errorf("--interval must be positive")
	}
	if opts.timeout <= 0 {
		return fmt.Errorf("--timeout must be positive")
	}
	return nil
}

func run(opts options) error {
	if err := validateOptions(opts); err != nil {
		return err
	}
	clientset, err := evaluator.NewClientset()
	if err != nil {
		return fmt.Errorf("Kubernetes client init: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	before, err := evaluator.CurrentDeploymentSchedulerInterval(ctx, clientset, opts.namespace, opts.deployment)
	if err != nil {
		return err
	}
	if before != opts.interval {
		if err := evaluator.ApplyDeploymentSchedulerInterval(
			ctx,
			clientset,
			opts.namespace,
			opts.deployment,
			opts.interval,
		); err != nil {
			return err
		}
	}
	after, err := evaluator.CurrentDeploymentSchedulerInterval(ctx, clientset, opts.namespace, opts.deployment)
	if err != nil {
		return err
	}
	if after != opts.interval {
		return fmt.Errorf("scheduler interval=%s after update, want %s", after, opts.interval)
	}

	fmt.Printf("scheduler interval restored: %s -> %s\n", before, after)
	return nil
}
