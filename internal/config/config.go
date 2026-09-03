// Package config loads per-repository settings.
//
// Reference ranges are not universal truths. A parser package sits higher on
// complexity than a service layer and should be allowed to; a codebase with a
// slow integration suite needs a different mutation budget. Without a way to
// say so, the only ways to get a green build are to ignore the tool or to
// argue with it, and both end with it being switched off.
//
// Ratcheting is the intended use on an existing codebase. Set the limit at
// where you are today, gate the delta at zero so nothing gets worse, and lower
// the limit as you clean up.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Name is the file metron looks for at the repository root.
const Name = "metron.json"

// File is the on-disk shape. Every field is optional; anything absent keeps the
// built-in default, so a config only has to say what differs.
type File struct {
	Complexity *ComplexitySection `json:"complexity,omitempty"`
	Mutation   *MutationSection   `json:"mutation,omitempty"`
	Graph      *GraphSection      `json:"graph,omitempty"`
}

type ComplexitySection struct {
	// MaxCognitive is the highest adjusted cognitive complexity a changed
	// function may reach.
	MaxCognitive *int `json:"maxCognitive,omitempty"`
	// MaxDelta is how much worse an already-existing function may get. Leaving
	// this at 0 is what makes a raised MaxCognitive a ratchet rather than a
	// surrender: today's complexity is tolerated, tomorrow's is not.
	MaxDelta *int `json:"maxDelta,omitempty"`
}

type MutationSection struct {
	MinScore    *float64 `json:"minScore,omitempty"`
	MinStrength *float64 `json:"minStrength,omitempty"`
	MinReach    *float64 `json:"minReach,omitempty"`
	Budget      *string  `json:"budget,omitempty"`
	Workers     *int     `json:"workers,omitempty"`
	MaxMutants  *int     `json:"maxMutants,omitempty"`
}

type GraphSection struct {
	MinSiblings *int `json:"minSiblings,omitempty"`
}

// Load reads metron.json from root. A missing file is not an error — the
// defaults are a complete configuration on their own.
func Load(root string) (*File, string, error) {
	path := filepath.Join(root, Name)
	buf, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &File{}, "", nil
	}
	if err != nil {
		return nil, "", err
	}

	var f File
	dec := json.NewDecoder(newTrimmer(buf))
	dec.DisallowUnknownFields() // a typo that silently does nothing is worse than an error
	if err := dec.Decode(&f); err != nil {
		return nil, "", fmt.Errorf("%s: %w", Name, err)
	}
	if err := f.validate(); err != nil {
		return nil, "", fmt.Errorf("%s: %w", Name, err)
	}
	return &f, path, nil
}

func (f *File) validate() error {
	if m := f.Mutation; m != nil {
		for name, v := range map[string]*float64{
			"minScore": m.MinScore, "minStrength": m.MinStrength, "minReach": m.MinReach,
		} {
			if v != nil && (*v < 0 || *v > 1) {
				return fmt.Errorf("mutation.%s must be between 0 and 1, got %v", name, *v)
			}
		}
		if m.Budget != nil {
			if _, err := time.ParseDuration(*m.Budget); err != nil {
				return fmt.Errorf("mutation.budget: %w", err)
			}
		}
		if m.Workers != nil && *m.Workers < 1 {
			return fmt.Errorf("mutation.workers must be at least 1, got %d", *m.Workers)
		}
	}
	if c := f.Complexity; c != nil {
		if c.MaxCognitive != nil && *c.MaxCognitive < 1 {
			return fmt.Errorf("complexity.maxCognitive must be at least 1, got %d", *c.MaxCognitive)
		}
		if c.MaxDelta != nil && *c.MaxDelta < 0 {
			return fmt.Errorf("complexity.maxDelta cannot be negative, got %d", *c.MaxDelta)
		}
	}
	return nil
}

// MutationBudget returns the configured budget, or fallback when unset.
func (f *File) MutationBudget(fallback time.Duration) time.Duration {
	if f.Mutation == nil || f.Mutation.Budget == nil {
		return fallback
	}
	d, err := time.ParseDuration(*f.Mutation.Budget)
	if err != nil {
		return fallback
	}
	return d
}
