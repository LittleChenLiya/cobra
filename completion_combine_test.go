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
	"reflect"
	"strings"
	"testing"
)

func namedProvider(name string, choices []Completion, directive ShellCompDirective) CompletionProvider {
	return CompletionProvider{Name: name, Func: FixedCompletions(choices, directive)}
}

func TestCombineCompletionsDuplicateCandidate(t *testing.T) {
	combiner := NewCompletionCombiner(
		namedProvider("first", []Completion{
			CompletionWithDesc("alpha", "from the first provider"),
			CompletionWithDesc("beta", "only in the first provider"),
		}, ShellCompDirectiveNoFileComp),
		namedProvider("second", []Completion{
			CompletionWithDesc("alpha", "from the second provider"),
			CompletionWithDesc("gamma", "only in the second provider"),
		}, ShellCompDirectiveNoFileComp),
	)

	result, err := combiner.Combine(nil, nil, "")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// The duplicate "alpha" candidate keeps the description of the
	// higher-priority (first) provider.
	expected := []Completion{
		"alpha\tfrom the first provider",
		"beta\tonly in the first provider",
		"gamma\tonly in the second provider",
	}
	if !reflect.DeepEqual(result.Completions, expected) {
		t.Errorf("expected: %v, got: %v", expected, result.Completions)
	}

	// Both providers are recorded as sources of "alpha".
	expectedSources := []CompletionSource{
		{Provider: "first", Value: "alpha"},
		{Provider: "first", Value: "beta"},
		{Provider: "second", Value: "alpha"},
		{Provider: "second", Value: "gamma"},
	}
	if !reflect.DeepEqual(result.Sources, expectedSources) {
		t.Errorf("expected sources: %v, got: %v", expectedSources, result.Sources)
	}

	if result.Directive != ShellCompDirectiveNoFileComp {
		t.Errorf("expected directive ShellCompDirectiveNoFileComp, got: %v", result.Directive)
	}
}

func TestCombineCompletionsDirectiveMerge(t *testing.T) {
	combiner := NewCompletionCombiner(
		namedProvider("nospace", []Completion{"one"}, ShellCompDirectiveNoSpace),
		namedProvider("keeporder", []Completion{"two"}, ShellCompDirectiveKeepOrder),
	)

	result, err := combiner.Combine(nil, nil, "")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	expectedDirective := ShellCompDirectiveNoSpace | ShellCompDirectiveKeepOrder
	if result.Directive != expectedDirective {
		t.Errorf("expected combined directive %d, got: %d", expectedDirective, result.Directive)
	}
	if !reflect.DeepEqual(result.Completions, []Completion{"one", "two"}) {
		t.Errorf("unexpected completions: %v", result.Completions)
	}
}

func TestCombineCompletionsConflictingDirectives(t *testing.T) {
	// A provider filtering file extensions cannot be combined with a
	// provider disabling file completion entirely.
	combiner := NewCompletionCombiner(
		namedProvider("exts", []Completion{"yaml", "json"}, ShellCompDirectiveFilterFileExt),
		namedProvider("nofile", []Completion{"plain"}, ShellCompDirectiveNoFileComp),
	)

	result, err := combiner.Combine(nil, nil, "")
	if err == nil {
		t.Fatal("Expected an error for mutually exclusive directives, got none")
	}
	if result != nil {
		t.Errorf("Expected no partial result on failure, got: %v", result)
	}
	if !strings.Contains(err.Error(), "ShellCompDirectiveNoFileComp") ||
		!strings.Contains(err.Error(), "ShellCompDirectiveFilterFileExt") {
		t.Errorf("Error should name the conflicting directives, got: %v", err)
	}

	// Through the CompletionFunc adapter the conflict is reported as an
	// explicit failure: no candidates and ShellCompDirectiveError.
	completions, directive := combiner.CompletionFunc()(nil, nil, "")
	if len(completions) != 0 {
		t.Errorf("Expected no candidates on failure, got: %v", completions)
	}
	if directive != ShellCompDirectiveError {
		t.Errorf("Expected ShellCompDirectiveError, got: %v", directive)
	}
}

func TestCombineCompletionsErrorDirectiveNotMasked(t *testing.T) {
	combiner := NewCompletionCombiner(
		namedProvider("ok", []Completion{"collected"}, ShellCompDirectiveDefault),
		namedProvider("broken", []Completion{"ignored"}, ShellCompDirectiveError),
	)

	result, err := combiner.Combine(nil, nil, "")
	if err == nil {
		t.Fatal("Expected an error when a provider returns ShellCompDirectiveError")
	}
	if result != nil {
		t.Errorf("Expected no half-collected batch of candidates, got: %v", result.Completions)
	}

	completions, directive := combiner.CompletionFunc()(nil, nil, "")
	if len(completions) != 0 {
		t.Errorf("Expected the error not to be masked by successful candidates, got: %v", completions)
	}
	if directive != ShellCompDirectiveError {
		t.Errorf("Expected ShellCompDirectiveError, got: %v", directive)
	}
}

func TestCombineCompletionsProviderCallOrder(t *testing.T) {
	var calls []string
	mkProvider := func(name string) CompletionProvider {
		return CompletionProvider{
			Name: name,
			Func: func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective) {
				calls = append(calls, name)
				return nil, ShellCompDirectiveDefault
			},
		}
	}
	combiner := NewCompletionCombiner(mkProvider("a"), mkProvider("b"), mkProvider("c"))

	if _, err := combiner.Combine(nil, nil, ""); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"a", "b", "c"}) {
		t.Errorf("Providers were not called in fixed order: %v", calls)
	}
}

func TestSplitCompletionSplitsOnlyOnce(t *testing.T) {
	value, desc := splitCompletion("value\tdescription with\textra tabs")
	if value != "value" {
		t.Errorf("expected value %q, got: %q", "value", value)
	}
	if desc != "description with\textra tabs" {
		t.Errorf("expected description to keep the remaining tabs, got: %q", desc)
	}

	value, desc = splitCompletion("plain")
	if value != "plain" || desc != "" {
		t.Errorf("unexpected split of description-less completion: %q, %q", value, desc)
	}
}

func TestCombineCompletionsKeepsMultiTabDescription(t *testing.T) {
	combiner := NewCompletionCombiner(
		namedProvider("tabs", []Completion{"value\tdesc\twith\ttabs"}, ShellCompDirectiveDefault),
		namedProvider("other", []Completion{"value\tother desc"}, ShellCompDirectiveDefault),
	)

	result, err := combiner.Combine(nil, nil, "")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	// The duplicate is detected on the value only, and the winning
	// candidate keeps its full, multi-tab description untouched.
	expected := []Completion{"value\tdesc\twith\ttabs"}
	if !reflect.DeepEqual(result.Completions, expected) {
		t.Errorf("expected: %v, got: %v", expected, result.Completions)
	}
}

// ExampleCompletionCombiner is a short entry point showing how to combine
// two completion providers and print the real completion output produced
// through Cobra's regular __complete command.
func ExampleCompletionCombiner() {
	combiner := NewCompletionCombiner(
		CompletionProvider{
			Name: "primary",
			Func: FixedCompletions([]Completion{
				CompletionWithDesc("alpha", "primary description"),
			}, ShellCompDirectiveNoFileComp),
		},
		CompletionProvider{
			Name: "secondary",
			Func: FixedCompletions([]Completion{
				CompletionWithDesc("alpha", "secondary description"),
				CompletionWithDesc("omega", "from the secondary provider"),
			}, ShellCompDirectiveNoFileComp),
		},
	)

	rootCmd := &Command{Use: "root", Run: func(*Command, []string) {}}
	rootCmd.ValidArgsFunction = combiner.CompletionFunc()

	buf := new(strings.Builder)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{ShellCompRequestCmd, ""})
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
	}
	fmt.Print(buf.String())

	// Output:
	// alpha	primary description
	// omega	from the secondary provider
	// :4
	// Completion ended with directive: ShellCompDirectiveNoFileComp
}
