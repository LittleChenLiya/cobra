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

// CompletionProvider pairs a name with a [CompletionFunc] so that a
// [CompletionCombiner] can record which provider contributed each candidate.
type CompletionProvider struct {
	// Name identifies the provider. It is recorded as the source of every
	// candidate the provider contributes.
	Name string
	// Func provides the completion candidates of this provider.
	Func CompletionFunc
}

// CompletionSource records that a provider contributed a candidate value
// to a combined completion result.
type CompletionSource struct {
	// Provider is the name of the provider that contributed the value.
	Provider string
	// Value is the candidate value, without any description.
	Value string
}

// CombinedCompletionResult is the merged outcome of a [CompletionCombiner].
type CombinedCompletionResult struct {
	// Completions holds the merged candidates in Cobra's usual format:
	// either "value" or "value\tdescription".
	Completions []Completion
	// Directive is the combination of the directives of all providers.
	Directive ShellCompDirective
	// Sources lists, in provider priority order, which provider contributed
	// which candidate value. A value offered by several providers is
	// recorded once per contributing provider.
	Sources []CompletionSource
}

// CompletionCombiner merges the results of several completion providers
// into a single result which keeps Cobra's regular completion format.
//
// The providers are always called in the order they were given, which is
// also their priority order: when several providers offer the same
// candidate value, the candidate (including its description) of the
// earliest provider wins, while the sources of every contributing provider
// are still recorded.
//
// The directives of the providers are OR-ed together, with two rules:
//   - if any provider returns [ShellCompDirectiveError], the whole
//     combination fails and none of the already collected candidates of
//     the other providers are returned;
//   - [ShellCompDirectiveNoFileComp] and [ShellCompDirectiveFilterFileExt]
//     are mutually exclusive, so combining providers that return both is
//     rejected with an explicit error.
type CompletionCombiner struct {
	providers []CompletionProvider
}

// NewCompletionCombiner creates a [CompletionCombiner] that merges the
// given providers. The providers are called in the given order, and
// earlier providers have priority over later ones for duplicate candidate
// values.
func NewCompletionCombiner(providers ...CompletionProvider) *CompletionCombiner {
	return &CompletionCombiner{providers: providers}
}

// Combine calls every provider in order and merges their results.
// It returns an error if any provider reports [ShellCompDirectiveError]
// or if the providers return mutually exclusive directives; in both cases
// no partial (half-collected) result is returned.
func (cc *CompletionCombiner) Combine(cmd *Command, args []string, toComplete string) (*CombinedCompletionResult, error) {
	result := &CombinedCompletionResult{}
	seen := make(map[string]struct{})
	var directive ShellCompDirective

	for _, provider := range cc.providers {
		completions, d := provider.Func(cmd, args, toComplete)
		if d&ShellCompDirectiveError != 0 {
			return nil, fmt.Errorf("completion provider %q returned %s", provider.Name, "ShellCompDirectiveError")
		}
		directive |= d
		if err := checkCombinedDirective(directive); err != nil {
			return nil, err
		}
		for _, comp := range completions {
			value, _ := splitCompletion(comp)
			result.Sources = append(result.Sources, CompletionSource{Provider: provider.Name, Value: value})
			if _, duplicate := seen[value]; duplicate {
				// A higher-priority provider already contributed this
				// value; keep its candidate and description.
				continue
			}
			seen[value] = struct{}{}
			result.Completions = append(result.Completions, comp)
		}
	}
	result.Directive = directive
	return result, nil
}

// CompletionFunc adapts the combiner to the regular single-callback
// completion entry points, such as [Command.ValidArgsFunction] and
// [Command.RegisterFlagCompletionFunc]. If the combination fails, no
// candidates are returned and the failure is reported through
// [ShellCompDirectiveError].
func (cc *CompletionCombiner) CompletionFunc() CompletionFunc {
	return func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		result, err := cc.Combine(cmd, args, toComplete)
		if err != nil {
			CompDebugln(fmt.Sprintf("completion combination failed: %v", err), false)
			return nil, ShellCompDirectiveError
		}
		return result.Completions, result.Directive
	}
}

// checkCombinedDirective rejects combinations of mutually exclusive
// directives.
func checkCombinedDirective(directive ShellCompDirective) error {
	if directive&ShellCompDirectiveNoFileComp != 0 && directive&ShellCompDirectiveFilterFileExt != 0 {
		return fmt.Errorf("cannot combine %s with %s",
			"ShellCompDirectiveNoFileComp", "ShellCompDirectiveFilterFileExt")
	}
	return nil
}

// splitCompletion splits a completion into its value and its description.
// Only the first TAB is a separator: any further TABs remain part of the
// description.
func splitCompletion(comp Completion) (value, description string) {
	parts := strings.SplitN(comp, "\t", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return comp, ""
}
