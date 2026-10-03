// Copyright 2013-2023 The Cobra Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cobra

import (
	"fmt"
	"strings"
)

// CompletionCandidate is a single completion value along with its optional
// description and the indices of the providers that contributed it.
//
// The Sources field records every provider that returned the same value,
// which allows callers to trace where a candidate came from even when its
// description was chosen from a higher-priority provider.
type CompletionCandidate struct {
	// Value is the completion choice without any description.
	Value string
	// Description is the textual description associated with the value.
	// It is empty when the provider did not supply one.
	Description string
	// Sources is the ordered list of provider indices (0-based) that
	// contributed this candidate. The first provider in the list is the
	// one whose description was kept.
	Sources []int
}

// CompletionResult is the detailed outcome of combining multiple completion
// providers. It preserves the candidate values, their descriptions, and the
// sources that contributed each candidate, while still exposing the combined
// directive.
type CompletionResult struct {
	// Candidates is the ordered list of combined candidates. Duplicate
	// values have been merged into a single entry.
	Candidates []CompletionCandidate
	// Directive is the combined shell completion directive.
	Directive ShellCompDirective
}

// Completions returns the candidates in Cobra's original completion format.
// Each candidate is rendered as the value, optionally followed by a TAB and
// its description. The returned slice can be used directly as a Completion
// result.
func (r CompletionResult) Completions() []Completion {
	comps := make([]Completion, 0, len(r.Candidates))
	for _, cand := range r.Candidates {
		if cand.Description != "" {
			comps = append(comps, CompletionWithDesc(cand.Value, cand.Description))
		} else {
			comps = append(comps, cand.Value)
		}
	}
	return comps
}

// CompletionCombiner merges the results of multiple completion providers into
// a single result. Providers are called in the order they are added, which
// establishes their priority: the first provider has the highest priority.
//
// The combiner is optional; it is only needed when a command wants to gather
// completions from more than one source. The original single-callback entry
// points (Command.ValidArgsFunction and Command.RegisterFlagCompletionFunc)
// are unchanged and continue to accept a single CompletionFunc. The
// CompletionFunc method adapts the combiner back to that interface.
type CompletionCombiner struct {
	providers []CompletionFunc
}

// NewCompletionCombiner creates a combiner that calls the given providers in
// the order they are provided. The first provider has the highest priority
// when resolving duplicate values with different descriptions.
func NewCompletionCombiner(providers ...CompletionFunc) *CompletionCombiner {
	return &CompletionCombiner{providers: providers}
}

// Add appends a provider to the combiner. Providers are always called in the
// order they were added.
func (c *CompletionCombiner) Add(provider CompletionFunc) *CompletionCombiner {
	c.providers = append(c.providers, provider)
	return c
}

// Combine calls each provider in order and merges the results.
//
// Candidates with the same value are merged: the description from the first
// provider (highest priority) is kept, while every contributing provider is
// recorded in Sources.
//
// Directives are combined with bitwise OR, with the following rules:
//   - ShellCompDirectiveError takes precedence over every other directive
//     and causes all candidates to be discarded (no half-batch is returned).
//   - ShellCompDirectiveNoFileComp and ShellCompDirectiveFilterFileExt are
//     mutually exclusive; combining them returns an error.
//   - Normal candidates combined with ShellCompDirectiveFilterFileExt are a
//     conflict and return an error, because the shell treats FilterFileExt
//     completions as file extension filters.
func (c *CompletionCombiner) Combine(cmd *Command, args []string, toComplete string) (CompletionResult, error) {
	var result CompletionResult

	directive := ShellCompDirective(0)
	hasError := false
	hasNoFileComp := false
	hasFilterFileExt := false
	errorProvider := -1

	// candidateMap tracks values we have already seen so that duplicates are
	// merged while preserving the first (highest-priority) description.
	candidateMap := make(map[string]*CompletionCandidate)
	candidateOrder := make([]string, 0)

	for i, provider := range c.providers {
		comps, dir := provider(cmd, args, toComplete)

		// Directives are combined with bitwise OR.
		directive |= dir

		if dir&ShellCompDirectiveError != 0 {
			hasError = true
			if errorProvider < 0 {
				errorProvider = i
			}
		}
		if dir&ShellCompDirectiveNoFileComp != 0 {
			hasNoFileComp = true
		}
		if dir&ShellCompDirectiveFilterFileExt != 0 {
			hasFilterFileExt = true
		}

		for _, comp := range comps {
			value, desc := splitCompletion(comp)
			if existing, ok := candidateMap[value]; ok {
				// Duplicate value: keep the first description and record the
				// additional source.
				existing.Sources = append(existing.Sources, i)
			} else {
				candidateMap[value] = &CompletionCandidate{
					Value:       value,
					Description: desc,
					Sources:     []int{i},
				}
				candidateOrder = append(candidateOrder, value)
			}
		}
	}

	// An error directive from any provider takes precedence and discards all
	// candidates so that no half-batch is returned.
	if hasError {
		result.Directive = ShellCompDirectiveError
		return result, fmt.Errorf("completion provider %d returned an error directive", errorProvider)
	}

	// NoFileComp and FilterFileExt are mutually exclusive.
	if hasNoFileComp && hasFilterFileExt {
		result.Directive = ShellCompDirectiveError
		return result, fmt.Errorf("completion directive conflict: ShellCompDirectiveNoFileComp and ShellCompDirectiveFilterFileExt are mutually exclusive")
	}

	// FilterFileExt completions are file extension filters; mixing them with
	// normal candidates is a conflict.
	if hasFilterFileExt {
		for _, value := range candidateOrder {
			if !strings.HasPrefix(value, ".") {
				result.Directive = ShellCompDirectiveError
				return result, fmt.Errorf("completion directive conflict: normal candidate %q conflicts with ShellCompDirectiveFilterFileExt", value)
			}
		}
	}

	// Build the candidate list in collection order.
	for _, value := range candidateOrder {
		result.Candidates = append(result.Candidates, *candidateMap[value])
	}
	result.Directive = directive

	return result, nil
}

// CompletionFunc returns a CompletionFunc that combines the providers. This
// adapts the combiner back to the original single-callback interface so it can
// be used with Command.ValidArgsFunction or
// Command.RegisterFlagCompletionFunc.
//
// If combining fails, the error is logged via CompErrorln and the
// ShellCompDirectiveError directive is returned with no candidates.
func (c *CompletionCombiner) CompletionFunc() CompletionFunc {
	return func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		result, err := c.Combine(cmd, args, toComplete)
		if err != nil {
			CompErrorln(err.Error())
			return nil, ShellCompDirectiveError
		}
		return result.Completions(), result.Directive
	}
}

// CombineCompletionFuncs returns a CompletionFunc that combines the given
// providers in the order they are provided. It is a convenience wrapper
// around NewCompletionCombiner(...).CompletionFunc().
func CombineCompletionFuncs(providers ...CompletionFunc) CompletionFunc {
	return NewCompletionCombiner(providers...).CompletionFunc()
}

// splitCompletion splits a completion into its value and description using the
// TAB delimiter. The split is performed at most once so that a description
// containing additional TAB characters is preserved verbatim.
func splitCompletion(comp Completion) (string, string) {
	parts := strings.SplitN(comp, "\t", 2)
	value := parts[0]
	desc := ""
	if len(parts) > 1 {
		desc = parts[1]
	}
	return value, desc
}
