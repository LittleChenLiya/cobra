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
	"testing"
)

// TestCompletionCombinerDuplicateValueDifferentDesc verifies that when two
// providers return the same value with different descriptions, only the
// description from the higher-priority (first) provider is kept, while both
// providers are recorded as sources.
func TestCompletionCombinerDuplicateValueDifferentDesc(t *testing.T) {
	provider0 := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{CompletionWithDesc("alpha", "First description")}, ShellCompDirectiveDefault
	}
	provider1 := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{CompletionWithDesc("alpha", "Second description")}, ShellCompDirectiveDefault
	}

	combiner := NewCompletionCombiner(provider0, provider1)
	result, err := combiner.Combine(nil, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d: %+v", len(result.Candidates), result.Candidates)
	}

	cand := result.Candidates[0]
	if cand.Value != "alpha" {
		t.Errorf("expected value %q, got %q", "alpha", cand.Value)
	}
	if cand.Description != "First description" {
		t.Errorf("expected description %q, got %q", "First description", cand.Description)
	}
	if len(cand.Sources) != 2 || cand.Sources[0] != 0 || cand.Sources[1] != 1 {
		t.Errorf("expected sources [0 1], got %v", cand.Sources)
	}
}

// TestCompletionCombinerNormalCandidatesConflictWithFilterFileExt verifies
// that mixing normal candidates with ShellCompDirectiveFilterFileExt returns
// an explicit failure, because the shell treats FilterFileExt completions as
// file extension filters.
func TestCompletionCombinerNormalCandidatesConflictWithFilterFileExt(t *testing.T) {
	normalProvider := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{"alpha", "beta"}, ShellCompDirectiveDefault
	}
	extProvider := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{".go", ".txt"}, ShellCompDirectiveFilterFileExt
	}

	combiner := NewCompletionCombiner(normalProvider, extProvider)
	result, err := combiner.Combine(nil, nil, "")
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
	if result.Directive != ShellCompDirectiveError {
		t.Errorf("expected directive %v, got %v", ShellCompDirectiveError, result.Directive)
	}
	if len(result.Candidates) != 0 {
		t.Errorf("expected no candidates on error, got %+v", result.Candidates)
	}
}

// TestCompletionCombinerNoFileCompConflictWithFilterFileExt verifies that
// ShellCompDirectiveNoFileComp and ShellCompDirectiveFilterFileExt are mutually
// exclusive and combining them returns an explicit failure.
func TestCompletionCombinerNoFileCompConflictWithFilterFileExt(t *testing.T) {
	noFileProvider := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return nil, ShellCompDirectiveNoFileComp
	}
	extProvider := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{".go"}, ShellCompDirectiveFilterFileExt
	}

	combiner := NewCompletionCombiner(noFileProvider, extProvider)
	result, err := combiner.Combine(nil, nil, "")
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
	if result.Directive != ShellCompDirectiveError {
		t.Errorf("expected directive %v, got %v", ShellCompDirectiveError, result.Directive)
	}
	if len(result.Candidates) != 0 {
		t.Errorf("expected no candidates on error, got %+v", result.Candidates)
	}
}

// TestCompletionCombinerTabSplitOnce verifies that a tab-separated description
// is split at most once, so a description containing additional tabs is
// preserved verbatim.
func TestCompletionCombinerTabSplitOnce(t *testing.T) {
	provider := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{"alpha\tdesc\twith\ttabs"}, ShellCompDirectiveDefault
	}

	combiner := NewCompletionCombiner(provider)
	result, err := combiner.Combine(nil, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(result.Candidates))
	}

	cand := result.Candidates[0]
	if cand.Value != "alpha" {
		t.Errorf("expected value %q, got %q", "alpha", cand.Value)
	}
	if cand.Description != "desc\twith\ttabs" {
		t.Errorf("expected description %q, got %q", "desc\twith\ttabs", cand.Description)
	}

	// Round-trip through Completions() must preserve the original format.
	comps := result.Completions()
	if len(comps) != 1 || comps[0] != "alpha\tdesc\twith\ttabs" {
		t.Errorf("expected completions [alpha\\tdesc\\twith\\ttabs], got %v", comps)
	}
}

// TestCompletionCombinerDirectiveCombination verifies that directives are
// combined with bitwise OR and that the combined directive reflects all
// providers.
func TestCompletionCombinerDirectiveCombination(t *testing.T) {
	provider0 := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{"alpha"}, ShellCompDirectiveNoSpace
	}
	provider1 := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{"beta"}, ShellCompDirectiveNoFileComp
	}
	provider2 := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{"gamma"}, ShellCompDirectiveKeepOrder
	}

	combiner := NewCompletionCombiner(provider0, provider1, provider2)
	result, err := combiner.Combine(nil, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := ShellCompDirectiveNoSpace | ShellCompDirectiveNoFileComp | ShellCompDirectiveKeepOrder
	if result.Directive != expected {
		t.Errorf("expected directive %v, got %v", expected, result.Directive)
	}
}

// TestCompletionCombinerErrorDirectiveTakesPrecedence verifies that an error
// directive from one provider is not overwritten by a success directive from
// another provider.
func TestCompletionCombinerErrorDirectiveTakesPrecedence(t *testing.T) {
	errorProvider := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return nil, ShellCompDirectiveError
	}
	successProvider := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{"alpha"}, ShellCompDirectiveDefault
	}

	combiner := NewCompletionCombiner(errorProvider, successProvider)
	result, err := combiner.Combine(nil, nil, "")
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
	if result.Directive != ShellCompDirectiveError {
		t.Errorf("expected directive %v, got %v", ShellCompDirectiveError, result.Directive)
	}
}

// TestCompletionCombinerErrorDiscardsHalfBatch verifies that when a provider
// returns an error, candidates collected from earlier providers are discarded
// (no half-batch is returned).
func TestCompletionCombinerErrorDiscardsHalfBatch(t *testing.T) {
	goodProvider := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{"alpha", "beta"}, ShellCompDirectiveDefault
	}
	errorProvider := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return nil, ShellCompDirectiveError
	}

	combiner := NewCompletionCombiner(goodProvider, errorProvider)
	result, err := combiner.Combine(nil, nil, "")
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
	if len(result.Candidates) != 0 {
		t.Errorf("expected no candidates on error, got %+v", result.Candidates)
	}
	if result.Directive != ShellCompDirectiveError {
		t.Errorf("expected directive %v, got %v", ShellCompDirectiveError, result.Directive)
	}
}

// TestCompletionCombinerDuplicateCandidates verifies that duplicate candidates
// are merged, the first description is kept, and sources are recorded.
func TestCompletionCombinerDuplicateCandidates(t *testing.T) {
	provider0 := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{
			CompletionWithDesc("alpha", "First alpha"),
			CompletionWithDesc("beta", "First beta"),
		}, ShellCompDirectiveDefault
	}
	provider1 := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{
			CompletionWithDesc("alpha", "Second alpha"),
			CompletionWithDesc("gamma", "Second gamma"),
		}, ShellCompDirectiveDefault
	}

	combiner := NewCompletionCombiner(provider0, provider1)
	result, err := combiner.Combine(nil, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d: %+v", len(result.Candidates), result.Candidates)
	}

	// alpha: merged, description from provider0, sources [0, 1].
	alpha := result.Candidates[0]
	if alpha.Value != "alpha" || alpha.Description != "First alpha" {
		t.Errorf("unexpected alpha candidate: %+v", alpha)
	}
	if len(alpha.Sources) != 2 || alpha.Sources[0] != 0 || alpha.Sources[1] != 1 {
		t.Errorf("expected alpha sources [0 1], got %v", alpha.Sources)
	}

	// beta: only from provider0, sources [0].
	beta := result.Candidates[1]
	if beta.Value != "beta" || beta.Description != "First beta" {
		t.Errorf("unexpected beta candidate: %+v", beta)
	}
	if len(beta.Sources) != 1 || beta.Sources[0] != 0 {
		t.Errorf("expected beta sources [0], got %v", beta.Sources)
	}

	// gamma: only from provider1, sources [1].
	gamma := result.Candidates[2]
	if gamma.Value != "gamma" || gamma.Description != "Second gamma" {
		t.Errorf("unexpected gamma candidate: %+v", gamma)
	}
	if len(gamma.Sources) != 1 || gamma.Sources[0] != 1 {
		t.Errorf("expected gamma sources [1], got %v", gamma.Sources)
	}
}

// TestCompletionCombinerCompletionFunc verifies that the CompletionFunc adapter
// produces the original completion format and can be used as a ValidArgsFunction.
func TestCompletionCombinerCompletionFunc(t *testing.T) {
	provider0 := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{CompletionWithDesc("alpha", "First")}, ShellCompDirectiveDefault
	}
	provider1 := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{CompletionWithDesc("alpha", "Second")}, ShellCompDirectiveNoSpace
	}

	combiner := NewCompletionCombiner(provider0, provider1)
	fn := combiner.CompletionFunc()

	comps, directive := fn(nil, nil, "")
	if directive != ShellCompDirectiveNoSpace {
		t.Errorf("expected directive %v, got %v", ShellCompDirectiveNoSpace, directive)
	}
	if len(comps) != 1 || comps[0] != "alpha\tFirst" {
		t.Errorf("expected completions [alpha\\tFirst], got %v", comps)
	}
}

// TestCompletionCombinerCombineCompletionFuncs verifies the convenience wrapper.
func TestCompletionCombinerCombineCompletionFuncs(t *testing.T) {
	provider0 := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{"alpha"}, ShellCompDirectiveDefault
	}
	provider1 := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{"beta"}, ShellCompDirectiveDefault
	}

	fn := CombineCompletionFuncs(provider0, provider1)
	comps, directive := fn(nil, nil, "")
	if directive != ShellCompDirectiveDefault {
		t.Errorf("expected directive %v, got %v", ShellCompDirectiveDefault, directive)
	}
	if len(comps) != 2 || comps[0] != "alpha" || comps[1] != "beta" {
		t.Errorf("expected completions [alpha beta], got %v", comps)
	}
}

// TestCompletionCombinerAdd verifies that providers can be added incrementally.
func TestCompletionCombinerAdd(t *testing.T) {
	provider0 := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{"alpha"}, ShellCompDirectiveDefault
	}
	provider1 := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{"beta"}, ShellCompDirectiveDefault
	}

	combiner := NewCompletionCombiner()
	combiner.Add(provider0).Add(provider1)

	result, err := combiner.Combine(nil, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Candidates) != 2 {
		t.Errorf("expected 2 candidates, got %d", len(result.Candidates))
	}
}

// TestCompletionCombinerEmpty verifies that an empty combiner returns an empty
// result with the default directive.
func TestCompletionCombinerEmpty(t *testing.T) {
	combiner := NewCompletionCombiner()
	result, err := combiner.Combine(nil, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Candidates) != 0 {
		t.Errorf("expected 0 candidates, got %d", len(result.Candidates))
	}
	if result.Directive != ShellCompDirectiveDefault {
		t.Errorf("expected directive %v, got %v", ShellCompDirectiveDefault, result.Directive)
	}
}

// TestCompletionCombinerRealCompletionOutput is a short entry that builds a
// command with a combined ValidArgsFunction, runs the real __complete command,
// and prints the actual completion output. This demonstrates the combiner
// producing Cobra's original completion format end to end.
func TestCompletionCombinerRealCompletionOutput(t *testing.T) {
	fruitProvider := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		var comps []Completion
		for _, c := range []Completion{
			CompletionWithDesc("apple", "A red fruit"),
			CompletionWithDesc("apricot", "A small orange fruit"),
			CompletionWithDesc("banana", "A yellow fruit"),
		} {
			if strings.HasPrefix(c, toComplete) {
				comps = append(comps, c)
			}
		}
		return comps, ShellCompDirectiveNoFileComp
	}
	colorProvider := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		var comps []Completion
		for _, c := range []Completion{
			CompletionWithDesc("apple", "A color of fruit"),
			CompletionWithDesc("azure", "A shade of blue"),
		} {
			if strings.HasPrefix(c, toComplete) {
				comps = append(comps, c)
			}
		}
		return comps, ShellCompDirectiveNoFileComp
	}

	rootCmd := &Command{
		Use: "picker",
		Run: emptyRun,
	}
	rootCmd.ValidArgsFunction = CombineCompletionFuncs(fruitProvider, colorProvider)

	output, err := executeCommand(rootCmd, ShellCompRequestCmd, "a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Print the real completion output for demonstration.
	fmt.Println("=== Real completion output ===")
	fmt.Println(output)
	fmt.Println("==============================")

	// The output should contain the merged candidates. "apple" appears once
	// with the description from the higher-priority fruit provider.
	if !strings.Contains(output, "apple\tA red fruit") {
		t.Errorf("expected output to contain %q, got:\n%s", "apple\tA red fruit", output)
	}
	if strings.Contains(output, "apple\tA color of fruit") {
		t.Errorf("output should not contain the lower-priority description for apple, got:\n%s", output)
	}
	if !strings.Contains(output, "apricot\tA small orange fruit") {
		t.Errorf("expected output to contain apricot, got:\n%s", output)
	}
	if !strings.Contains(output, "azure\tA shade of blue") {
		t.Errorf("expected output to contain azure, got:\n%s", output)
	}
	// banana does not match the toComplete prefix "a", so it should not appear.
	if strings.Contains(output, "banana") {
		t.Errorf("output should not contain banana (does not match prefix), got:\n%s", output)
	}
	// The directive should be NoFileComp (4).
	if !strings.Contains(output, ":4\n") {
		t.Errorf("expected output to contain directive :4, got:\n%s", output)
	}
}

// TestCompletionCombinerRealCompletionOutputNoDesc demonstrates the
// __completeNoDesc path, which strips descriptions.
func TestCompletionCombinerRealCompletionOutputNoDesc(t *testing.T) {
	fruitProvider := func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
		return []Completion{
			CompletionWithDesc("apple", "A red fruit"),
			CompletionWithDesc("banana", "A yellow fruit"),
		}, ShellCompDirectiveNoFileComp
	}

	rootCmd := &Command{
		Use: "picker",
		Run: emptyRun,
	}
	rootCmd.ValidArgsFunction = CombineCompletionFuncs(fruitProvider)

	output, err := executeCommand(rootCmd, ShellCompNoDescRequestCmd, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fmt.Println("=== Real completion output (no descriptions) ===")
	fmt.Println(output)
	fmt.Println("===============================================")

	// Descriptions should be stripped.
	if strings.Contains(output, "\t") {
		t.Errorf("output should not contain descriptions in NoDesc mode, got:\n%s", output)
	}
	if !strings.Contains(output, "apple") {
		t.Errorf("expected output to contain apple, got:\n%s", output)
	}
	if !strings.Contains(output, "banana") {
		t.Errorf("expected output to contain banana, got:\n%s", output)
	}
}
