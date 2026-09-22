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
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	flag "github.com/spf13/pflag"
)

func findFlagContract(t *testing.T, node *CommandNode, name string) FlagContract {
	t.Helper()
	for _, f := range node.Flags {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no flag %q in contract of command %q", name, node.Path)
	return FlagContract{}
}

func findChildNode(t *testing.T, node *CommandNode, name string) *CommandNode {
	t.Helper()
	for _, child := range node.Commands {
		if child.Name == name {
			return child
		}
	}
	t.Fatalf("no child command %q under %q", name, node.Path)
	return nil
}

// TestExportContractShadowing covers a persistent flag on the root being
// shadowed by a local flag of the same name on the child, plus aliases and
// a required-together group.
func TestExportContractShadowing(t *testing.T) {
	root := &Command{Use: "root"}
	root.PersistentFlags().String("region", "us-east-1", "deployment region")
	root.PersistentFlags().Bool("verbose", false, "verbose output")

	child := &Command{
		Use:     "child",
		Aliases: []string{"ch", "kid"},
		Run:     func(*Command, []string) {},
	}
	child.Flags().String("region", "eu-west-1", "local region override")
	child.Flags().String("token", "", "auth token")
	child.Flags().String("endpoint", "", "service endpoint")
	child.MarkFlagsRequiredTogether("token", "endpoint")
	root.AddCommand(child)

	contract, err := root.ExportContract(nil)
	if err != nil {
		t.Fatalf("ExportContract: %v", err)
	}
	if contract.Version != ContractVersion {
		t.Fatalf("expected version %d, got %d", ContractVersion, contract.Version)
	}

	childNode := findChildNode(t, contract.Root, "child")
	if childNode.Path != "root child" {
		t.Fatalf("unexpected child path %q", childNode.Path)
	}
	if got := strings.Join(childNode.Aliases, " "); got != "ch kid" {
		t.Fatalf("unexpected aliases %q", got)
	}

	// The shadowing local flag wins and its origin is traceable.
	region := findFlagContract(t, childNode, "region")
	if region.Source != FlagSourceLocal {
		t.Errorf("region: expected source %q, got %q", FlagSourceLocal, region.Source)
	}
	if region.DefinedBy != "root child" {
		t.Errorf("region: expected defined_by %q, got %q", "root child", region.DefinedBy)
	}
	if region.Shadows != "root" {
		t.Errorf("region: expected shadows %q, got %q", "root", region.Shadows)
	}
	if region.Default != "eu-west-1" || region.Type != "string" {
		t.Errorf("region: unexpected default/type %q/%q", region.Default, region.Type)
	}

	// The inherited, non-shadowed persistent flag is exported as inherited.
	verbose := findFlagContract(t, childNode, "verbose")
	if verbose.Source != FlagSourceInherited || verbose.DefinedBy != "root" || !verbose.Persistent {
		t.Errorf("verbose: unexpected contract %+v", verbose)
	}

	// The root's own view of region is its persistent flag, not the child's.
	rootRegion := findFlagContract(t, contract.Root, "region")
	if rootRegion.Default != "us-east-1" || rootRegion.Source != FlagSourceLocal || !rootRegion.Persistent {
		t.Errorf("root region: unexpected contract %+v", rootRegion)
	}

	// The required-together group is exported.
	if len(childNode.FlagGroups) != 1 {
		t.Fatalf("expected 1 flag group, got %d", len(childNode.FlagGroups))
	}
	group := childNode.FlagGroups[0]
	if group.Kind != FlagGroupRequiredTogether || strings.Join(group.Flags, " ") != "token endpoint" {
		t.Errorf("unexpected flag group %+v", group)
	}
}

// panicValue is a flag.Value whose Set panics, proving the export never
// invokes custom Value.Set implementations.
type panicValue struct{ v string }

func (p *panicValue) String() string   { return p.v }
func (p *panicValue) Type() string     { return "panic" }
func (p *panicValue) Set(string) error { panic("Value.Set must not be called during export") }

// TestExportContractDoesNotExecuteHooks replaces every executable hook with
// one that fails the test when called, and verifies the export still works.
func TestExportContractDoesNotExecuteHooks(t *testing.T) {
	fail := func(string) { panic("hook must not be called during export") }

	root := &Command{
		Use: "root",
		PersistentPreRun: func(*Command, []string) {
			fail("PersistentPreRun")
		},
	}
	child := &Command{
		Use: "child",
		Args: func(*Command, []string) error {
			fail("Args")
			return nil
		},
		ValidArgsFunction: func(*Command, []string, string) ([]string, ShellCompDirective) {
			fail("ValidArgsFunction")
			return nil, ShellCompDirectiveError
		},
		PreRun: func(*Command, []string) { fail("PreRun") },
		Run:    func(*Command, []string) { fail("Run") },
	}
	child.Flags().Var(&panicValue{v: "x"}, "custom", "custom value flag")
	root.AddCommand(child)

	contract, err := root.ExportContract(nil)
	if err != nil {
		t.Fatalf("ExportContract: %v", err)
	}
	childNode := findChildNode(t, contract.Root, "child")
	if childNode.ArgsValidation != unknownValue {
		t.Errorf("expected args_validation %q, got %q", unknownValue, childNode.ArgsValidation)
	}
	custom := findFlagContract(t, childNode, "custom")
	if custom.Type != "panic" {
		t.Errorf("expected type %q, got %q", "panic", custom.Type)
	}
}

func TestExportContractHiddenAndDeprecated(t *testing.T) {
	root := &Command{Use: "root"}
	hidden := &Command{Use: "hidden", Hidden: true, Run: func(*Command, []string) {}}
	deprecated := &Command{Use: "old", Deprecated: "use new instead", Run: func(*Command, []string) {}}
	visible := &Command{Use: "visible", Run: func(*Command, []string) {}}
	root.AddCommand(hidden, deprecated, visible)

	contract, err := root.ExportContract(nil)
	if err != nil {
		t.Fatalf("ExportContract: %v", err)
	}
	if len(contract.Root.Commands) != 1 || contract.Root.Commands[0].Name != "visible" {
		t.Fatalf("expected only the visible command, got %+v", contract.Root.Commands)
	}

	contract, err = root.ExportContract(&ExportOptions{IncludeHidden: true})
	if err != nil {
		t.Fatalf("ExportContract: %v", err)
	}
	if len(contract.Root.Commands) != 2 {
		t.Fatalf("expected 2 commands with IncludeHidden, got %d", len(contract.Root.Commands))
	}
	if !findChildNode(t, contract.Root, "hidden").Hidden {
		t.Error("hidden command not marked hidden in contract")
	}

	contract, err = root.ExportContract(&ExportOptions{IncludeHidden: true, IncludeDeprecated: true})
	if err != nil {
		t.Fatalf("ExportContract: %v", err)
	}
	if len(contract.Root.Commands) != 3 {
		t.Fatalf("expected 3 commands with both options, got %d", len(contract.Root.Commands))
	}
	if got := findChildNode(t, contract.Root, "old").Deprecated; got != "use new instead" {
		t.Errorf("unexpected deprecation message %q", got)
	}
}

// TestExportContractInheritedFlagGroups verifies that flag groups declared
// on a parent's persistent flags are exported for the child as well.
func TestExportContractInheritedFlagGroups(t *testing.T) {
	root := &Command{Use: "root"}
	root.PersistentFlags().String("cert", "", "cert file")
	root.PersistentFlags().String("key", "", "key file")
	root.PersistentFlags().String("format", "", "output format")
	root.PersistentFlags().String("output", "", "output file")
	root.MarkFlagsRequiredTogether("cert", "key")
	root.MarkFlagsMutuallyExclusive("format", "output")

	child := &Command{Use: "child", Run: func(*Command, []string) {}}
	root.AddCommand(child)

	contract, err := root.ExportContract(nil)
	if err != nil {
		t.Fatalf("ExportContract: %v", err)
	}
	for _, node := range []*CommandNode{contract.Root, findChildNode(t, contract.Root, "child")} {
		if len(node.FlagGroups) != 2 {
			t.Fatalf("%q: expected 2 flag groups, got %+v", node.Path, node.FlagGroups)
		}
		kinds := map[string][]string{}
		for _, g := range node.FlagGroups {
			kinds[g.Kind] = g.Flags
		}
		if got := strings.Join(kinds[FlagGroupRequiredTogether], " "); got != "cert key" {
			t.Errorf("%q: unexpected required-together group %q", node.Path, got)
		}
		if got := strings.Join(kinds[FlagGroupMutuallyExclusive], " "); got != "format output" {
			t.Errorf("%q: unexpected mutually-exclusive group %q", node.Path, got)
		}
	}
}

func TestExportContractOneRequiredGroup(t *testing.T) {
	root := &Command{Use: "root"}
	root.Flags().String("json", "", "json input")
	root.Flags().String("yaml", "", "yaml input")
	root.MarkFlagsOneRequired("json", "yaml")

	contract, err := root.ExportContract(nil)
	if err != nil {
		t.Fatalf("ExportContract: %v", err)
	}
	if len(contract.Root.FlagGroups) != 1 ||
		contract.Root.FlagGroups[0].Kind != FlagGroupOneRequired ||
		strings.Join(contract.Root.FlagGroups[0].Flags, " ") != "json yaml" {
		t.Fatalf("unexpected flag groups %+v", contract.Root.FlagGroups)
	}
}

func TestExportContractDuplicateAlias(t *testing.T) {
	root := &Command{Use: "root"}
	child := &Command{Use: "child", Aliases: []string{"c", "c"}}
	root.AddCommand(child)

	_, err := root.ExportContract(nil)
	if err == nil {
		t.Fatal("expected an error for duplicate aliases")
	}
	if !strings.Contains(err.Error(), `"root child"`) || !strings.Contains(err.Error(), `"c"`) {
		t.Fatalf("error should contain the command path and alias, got: %v", err)
	}
}

func TestExportContractCommandCycle(t *testing.T) {
	// Child cycle: the root is its own descendant. AddCommand itself cannot
	// be used to close the cycle, because it eagerly computes CommandPath,
	// so the cycle is constructed directly.
	root := &Command{Use: "root"}
	child := &Command{Use: "child"}
	root.AddCommand(child)
	child.commands = append(child.commands, root)

	if _, err := root.ExportContract(nil); err == nil {
		t.Fatal("expected an error for a cyclic command tree")
	} else if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected a cycle error, got: %v", err)
	}

	// Parent chain cycle.
	a := &Command{Use: "a"}
	b := &Command{Use: "b"}
	a.AddCommand(b)
	a.parent = b

	if _, err := a.ExportContract(nil); err == nil {
		t.Fatal("expected an error for a cyclic parent chain")
	} else if !strings.Contains(err.Error(), "cycle") || !strings.Contains(err.Error(), `"a"`) {
		t.Fatalf("expected a cycle error containing the command path, got: %v", err)
	}
}

func TestExportContractInvalidGroupMember(t *testing.T) {
	root := &Command{Use: "root"}
	root.Flags().String("token", "", "auth token")
	if err := root.Flags().SetAnnotation("token", requiredAsGroupAnnotation, []string{"token missing"}); err != nil {
		t.Fatal(err)
	}

	_, err := root.ExportContract(nil)
	if err == nil {
		t.Fatal("expected an error for a group with an undefined member")
	}
	if !strings.Contains(err.Error(), `"root"`) || !strings.Contains(err.Error(), `"missing"`) {
		t.Fatalf("error should contain the command path and the undefined flag, got: %v", err)
	}
}

// TestExportContractDeterministic verifies that repeated exports are
// byte-identical and leave the command tree and flag state untouched.
func TestExportContractDeterministic(t *testing.T) {
	root := &Command{Use: "root"}
	root.PersistentFlags().String("region", "us-east-1", "deployment region")
	child := &Command{Use: "child", Aliases: []string{"ch"}, Run: func(*Command, []string) {}}
	child.Flags().String("region", "eu-west-1", "local region override")
	child.Flags().Int("count", 3, "repetitions")
	root.AddCommand(child)

	// Simulate prior use: one flag was set on the command line.
	if err := child.Flags().Set("count", "5"); err != nil {
		t.Fatal(err)
	}

	snapshot := func() string {
		var sb strings.Builder
		child.Flags().VisitAll(func(f *flag.Flag) {
			fmt.Fprintf(&sb, "%s=%s changed=%t def=%s\n", f.Name, f.Value.String(), f.Changed, f.DefValue)
		})
		root.PersistentFlags().VisitAll(func(f *flag.Flag) {
			fmt.Fprintf(&sb, "%s=%s changed=%t def=%s\n", f.Name, f.Value.String(), f.Changed, f.DefValue)
		})
		return sb.String()
	}
	before := snapshot()

	var first, second bytes.Buffer
	if err := root.WriteContract(&first, nil); err != nil {
		t.Fatalf("WriteContract: %v", err)
	}
	if err := root.WriteContract(&second, nil); err != nil {
		t.Fatalf("WriteContract: %v", err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("repeated exports are not byte-identical")
	}
	if after := snapshot(); after != before {
		t.Fatalf("flag state changed by export:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func ExampleCommand_ExportContract() {
	root := &Command{Use: "serve"}
	root.PersistentFlags().String("region", "us-east-1", "deployment region")

	child := &Command{
		Use:     "api",
		Aliases: []string{"a"},
		Short:   "Serve the API",
		Run:     func(*Command, []string) {},
	}
	child.Flags().String("region", "eu-west-1", "local region override")
	child.Flags().String("token", "", "auth token")
	child.Flags().String("endpoint", "", "service endpoint")
	child.MarkFlagsRequiredTogether("token", "endpoint")
	root.AddCommand(child)

	if err := root.WriteContract(os.Stdout, nil); err != nil {
		fmt.Println(err)
	}

	// A flag group referencing an undefined flag is reported with the
	// command path.
	broken := &Command{Use: "broken"}
	broken.Flags().String("token", "", "auth token")
	_ = broken.Flags().SetAnnotation("token", requiredAsGroupAnnotation, []string{"token missing"})
	if _, err := broken.ExportContract(nil); err != nil {
		fmt.Println(err)
	}

	// Output:
	// {
	//   "version": 1,
	//   "root": {
	//     "name": "serve",
	//     "path": "serve",
	//     "use": "serve",
	//     "flags": [
	//       {
	//         "name": "region",
	//         "usage": "deployment region",
	//         "type": "string",
	//         "default": "us-east-1",
	//         "persistent": true,
	//         "source": "local",
	//         "defined_by": "serve"
	//       }
	//     ],
	//     "commands": [
	//       {
	//         "name": "api",
	//         "path": "serve api",
	//         "use": "api",
	//         "short": "Serve the API",
	//         "aliases": [
	//           "a"
	//         ],
	//         "flags": [
	//           {
	//             "name": "endpoint",
	//             "usage": "service endpoint",
	//             "type": "string",
	//             "default": "",
	//             "source": "local",
	//             "defined_by": "serve api"
	//           },
	//           {
	//             "name": "region",
	//             "usage": "local region override",
	//             "type": "string",
	//             "default": "eu-west-1",
	//             "source": "local",
	//             "defined_by": "serve api",
	//             "shadows": "serve"
	//           },
	//           {
	//             "name": "token",
	//             "usage": "auth token",
	//             "type": "string",
	//             "default": "",
	//             "source": "local",
	//             "defined_by": "serve api"
	//           }
	//         ],
	//         "flag_groups": [
	//           {
	//             "kind": "required_together",
	//             "flags": [
	//               "token",
	//               "endpoint"
	//             ]
	//           }
	//         ]
	//       }
	//     ]
	//   }
	// }
	// cobra: contract export: command "broken" has a required_together flag group [token missing] referencing undefined flag "missing"
}
