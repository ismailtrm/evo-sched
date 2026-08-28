package main

import (
	"testing"
	"time"
)

func TestValidateOptions(t *testing.T) {
	valid := options{
		namespace:  "evo-sched",
		deployment: "evok8s-scheduler",
		interval:   2 * time.Second,
		timeout:    time.Minute,
	}
	if err := validateOptions(valid); err != nil {
		t.Fatalf("validateOptions() error = %v", err)
	}

	tests := []struct {
		name string
		edit func(*options)
	}{
		{name: "empty namespace", edit: func(opts *options) { opts.namespace = "" }},
		{name: "empty deployment", edit: func(opts *options) { opts.deployment = "" }},
		{name: "zero interval", edit: func(opts *options) { opts.interval = 0 }},
		{name: "zero timeout", edit: func(opts *options) { opts.timeout = 0 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := valid
			tt.edit(&opts)
			if err := validateOptions(opts); err == nil {
				t.Fatal("validateOptions() error = nil, want rejection")
			}
		})
	}
}
