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
	"errors"
	"fmt"
	"strings"
	"testing"

	flag "github.com/spf13/pflag"
)

func findSubCommand(t *testing.T, cc *CommandContract, name string) *CommandContract {
	t.Helper()
	for _, sub := range cc.Commands {
		if sub.Name == name {
			return sub
		}
	}
	t.Fatalf("exported command %q has no subcommand %q", cc.Path, name)
	return nil
}

func findContractFlag(t *testing.T, cc *CommandContract, name string) FlagContract {
	t.Helper()
	for _, f := range cc.Flags {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("exported command %q has no flag %q", cc.Path, name)
	return FlagContract{}
}

func countFlags(cc *CommandContract, name string) int {
	n := 0
	for _, f := range cc.Flags {
		if f.Name == name {
			n++
		}
	}
	return n
}

func TestExportContractShadowingAliasAndRequiredTogether(t *testing.T) {
	root := &Command{Use: "root"}
	root.PersistentFlags().String("region", "us-east-1", "root region")

	child := &Command{Use: "child", Aliases: []string{"ch"}, Run: func(*Command, []string) {}}
	child.Flags().String("region", "eu-west-1", "child region")
	child.Flags().String("token", "", "auth token")
	child.Flags().String("user", "", "user name")
	child.MarkFlagsRequiredTogether("token", "user")
	child.MarkFlagRequired("token")

	grandchild := &Command{Use: "grandchild", Run: func(*Command, []string) {}}
	child.AddCommand(grandchild)
	root.AddCommand(child)

	contract, err := root.ExportContract(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if contract.Version != ContractVersion {
		t.Errorf("expected version %d, got %d", ContractVersion, contract.Version)
	}

	rootRegion := findContractFlag(t, contract.Root, "region")
	if rootRegion.Source != FlagSourceLocal || rootRegion.DefinedAt != "root" || rootRegion.Default != "us-east-1" {
		t.Errorf("unexpected root region flag: %+v", rootRegion)
	}

	ec := findSubCommand(t, contract.Root, "child")
	if ec.Path != "root child" {
		t.Errorf("expected path %q, got %q", "root child", ec.Path)
	}
	if len(ec.Aliases) != 1 || ec.Aliases[0] != "ch" {
		t.Errorf("expected aliases [ch], got %v", ec.Aliases)
	}
	if !ec.Runnable {
		t.Error("expected child to be runnable")
	}

	// The local --region shadows the inherited persistent one: exactly one
	// effective flag, sourced locally and traceable to the child.
	if n := countFlags(ec, "region"); n != 1 {
		t.Fatalf("expected exactly one effective region flag, got %d", n)
	}
	region := findContractFlag(t, ec, "region")
	if region.Source != FlagSourceLocal {
		t.Errorf("expected shadowing region flag to be local, got %q", region.Source)
	}
	if region.DefinedAt != "root child" {
		t.Errorf("expected region defined_at %q, got %q", "root child", region.DefinedAt)
	}
	if region.Default != "eu-west-1" || region.Type != "string" {
		t.Errorf("unexpected region flag: %+v", region)
	}
	if region.CurrentValue != UnknownValue {
		t.Errorf("expected current value %q, got %q", UnknownValue, region.CurrentValue)
	}

	token := findContractFlag(t, ec, "token")
	if !token.Required {
		t.Error("expected token flag to be required")
	}

	if len(ec.FlagGroups) != 1 {
		t.Fatalf("expected one flag group, got %v", ec.FlagGroups)
	}
	group := ec.FlagGroups[0]
	if group.Type != FlagGroupRequiredTogether {
		t.Errorf("expected group type %q, got %q", FlagGroupRequiredTogether, group.Type)
	}
	if strings.Join(group.Flags, " ") != "token user" {
		t.Errorf("expected group flags [token user], got %v", group.Flags)
	}

	// The grandchild does not see the child's local flag; it inherits the
	// root's persistent --region, traceable back to the root.
	eg := findSubCommand(t, ec, "grandchild")
	inherited := findContractFlag(t, eg, "region")
	if inherited.Source != FlagSourceInherited {
		t.Errorf("expected grandchild region to be inherited, got %q", inherited.Source)
	}
	if inherited.DefinedAt != "root" {
		t.Errorf("expected grandchild region defined_at %q, got %q", "root", inherited.DefinedAt)
	}
	if inherited.Default != "us-east-1" {
		t.Errorf("expected grandchild region default %q, got %q", "us-east-1", inherited.Default)
	}
}

func TestExportContractDoesNotInvokeUserCode(t *testing.T) {
	fail := func() {
		t.Fatal("export invoked user code")
	}
	root := &Command{
		Use: "root",
		Args: func(*Command, []string) error {
			fail()
			return errors.New("must not be called")
		},
		ValidArgsFunction: func(*Command, []string, string) ([]Completion, ShellCompDirective) {
			fail()
			return nil, ShellCompDirectiveError
		},
	}
	child := &Command{
		Use: "child",
		Run: func(*Command, []string) { fail() },
		PreRun: func(*Command, []string) {
			fail()
		},
	}
	grandchild := &Command{
		Use: "grandchild",
		RunE: func(*Command, []string) error {
			fail()
			return errors.New("must not be called")
		},
		PersistentPreRunE: func(*Command, []string) error {
			fail()
			return errors.New("must not be called")
		},
	}
	child.AddCommand(grandchild)
	root.AddCommand(child)

	if _, err := root.ExportContract(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExportContractHiddenAndDeprecated(t *testing.T) {
	newRoot := func() *Command {
		root := &Command{Use: "root"}
		root.AddCommand(&Command{Use: "visible", Run: func(*Command, []string) {}})
		root.AddCommand(&Command{Use: "secret", Hidden: true, Run: func(*Command, []string) {}})
		root.AddCommand(&Command{Use: "old", Deprecated: "use visible instead", Run: func(*Command, []string) {}})
		return root
	}

	names := func(contract *Contract) []string {
		var out []string
		for _, sub := range contract.Root.Commands {
			out = append(out, sub.Name)
		}
		return out
	}

	contract, err := newRoot().ExportContract(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(names(contract), " "); got != "visible" {
		t.Errorf("expected only [visible] by default, got [%s]", got)
	}

	contract, err = newRoot().ExportContract(&ExportOptions{IncludeHidden: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(names(contract), " "); got != "secret visible" {
		t.Errorf("expected [secret visible] with IncludeHidden, got [%s]", got)
	}
	if !findSubCommand(t, contract.Root, "secret").Hidden {
		t.Error("expected hidden command to be marked hidden")
	}

	contract, err = newRoot().ExportContract(&ExportOptions{IncludeHidden: true, IncludeDeprecated: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(names(contract), " "); got != "old secret visible" {
		t.Errorf("expected [old secret visible], got [%s]", got)
	}
	if got := findSubCommand(t, contract.Root, "old").Deprecated; got != "use visible instead" {
		t.Errorf("expected deprecation message, got %q", got)
	}
}

func TestExportContractInheritedFlagGroup(t *testing.T) {
	root := &Command{Use: "root"}
	root.PersistentFlags().String("cert", "", "cert file")
	root.PersistentFlags().String("key", "", "key file")
	root.MarkFlagsOneRequired("cert", "key")

	child := &Command{Use: "child", Run: func(*Command, []string) {}}
	root.AddCommand(child)

	contract, err := root.ExportContract(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ec := findSubCommand(t, contract.Root, "child")
	if len(ec.FlagGroups) != 1 {
		t.Fatalf("expected inherited flag group on child, got %v", ec.FlagGroups)
	}
	group := ec.FlagGroups[0]
	if group.Type != FlagGroupOneRequired || strings.Join(group.Flags, " ") != "cert key" {
		t.Errorf("unexpected inherited group: %+v", group)
	}
}

func TestExportContractDuplicateAlias(t *testing.T) {
	for _, aliases := range [][]string{{"dup", "dup"}, {"child"}} {
		root := &Command{Use: "root"}
		root.AddCommand(&Command{Use: "child", Aliases: aliases, Run: func(*Command, []string) {}})

		_, err := root.ExportContract(nil)
		if err == nil {
			t.Fatalf("expected duplicate alias error for aliases %v", aliases)
		}
		if !strings.Contains(err.Error(), `"root child"`) {
			t.Errorf("expected error to contain command path, got: %v", err)
		}
		if !strings.Contains(err.Error(), "duplicate alias") {
			t.Errorf("expected duplicate alias error, got: %v", err)
		}
	}
}

func TestExportContractCycle(t *testing.T) {
	a := &Command{Use: "a"}
	b := &Command{Use: "b"}
	a.AddCommand(b)
	// AddCommand itself cannot be used to close a cycle (it recurses on
	// CommandPath), so construct the cyclic parent link directly.
	b.commands = append(b.commands, a)
	a.parent = b

	if _, err := a.ExportContract(nil); err == nil {
		t.Fatal("expected cycle error")
	} else if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected cycle error, got: %v", err)
	}

	// A cycle that does not include the exported root must also be caught.
	root := &Command{Use: "root"}
	c := &Command{Use: "c"}
	d := &Command{Use: "d"}
	root.AddCommand(c)
	c.AddCommand(d)
	d.commands = append(d.commands, c)
	c.parent = d

	if _, err := root.ExportContract(nil); err == nil {
		t.Fatal("expected cycle error")
	} else if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected cycle error, got: %v", err)
	}
}

func TestExportContractInvalidGroupMember(t *testing.T) {
	root := &Command{Use: "root"}
	child := &Command{Use: "child", Run: func(*Command, []string) {}}
	child.Flags().String("token", "", "auth token")
	if err := child.Flags().SetAnnotation("token", requiredAsGroupAnnotation, []string{"token missing"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	root.AddCommand(child)

	_, err := root.ExportContract(nil)
	if err == nil {
		t.Fatal("expected invalid group member error")
	}
	for _, want := range []string{`"root child"`, `"missing"`, "required_together"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected error to contain %s, got: %v", want, err)
		}
	}
}

func flagStateSnapshot(c *Command) string {
	var sb strings.Builder
	sb.WriteString(c.Name())
	sb.WriteString(":")
	// LocalFlags triggers Cobra's standard lazy persistent-flag merge, so the
	// snapshot reflects flag values and Changed state, not internal caching.
	c.LocalFlags().VisitAll(func(f *flag.Flag) {
		fmt.Fprintf(&sb, "%s=%s(changed=%t);", f.Name, f.Value.String(), f.Changed)
	})
	for _, sub := range c.Commands() {
		sb.WriteString(flagStateSnapshot(sub))
	}
	return sb.String()
}

func TestExportContractDeterministicAndNonMutating(t *testing.T) {
	root := &Command{Use: "root"}
	root.PersistentFlags().String("region", "us-east-1", "root region")
	child := &Command{Use: "child", Aliases: []string{"ch", "c2"}, Run: func(*Command, []string) {}}
	child.Flags().String("region", "eu-west-1", "child region")
	child.Flags().Int("port", 8080, "port")
	child.Flags().Bool("verbose", false, "verbose")
	child.MarkFlagsMutuallyExclusive("port", "verbose")
	root.AddCommand(child)

	before := flagStateSnapshot(root)

	first, err := root.ExportContractJSON(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := root.ExportContractJSON(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("expected repeated exports to be byte-identical")
	}

	if after := flagStateSnapshot(root); after != before {
		t.Errorf("export mutated flag state:\nbefore: %s\nafter:  %s", before, after)
	}
}

func ExampleCommand_ExportContractJSON() {
	root := &Command{Use: "app", Short: "demo app"}
	root.PersistentFlags().String("region", "us", "region to use")
	serve := &Command{
		Use:     "serve",
		Aliases: []string{"s"},
		Run:     func(*Command, []string) {},
	}
	serve.Flags().Int("port", 8080, "port to listen on")
	root.AddCommand(serve)

	out, err := root.ExportContractJSON(nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(out))
	// Output:
	// {
	//   "version": 1,
	//   "root": {
	//     "name": "app",
	//     "path": "app",
	//     "use": "app",
	//     "short": "demo app",
	//     "runnable": false,
	//     "flags": [
	//       {
	//         "name": "region",
	//         "usage": "region to use",
	//         "type": "string",
	//         "default": "us",
	//         "current_value": "unknown",
	//         "required": false,
	//         "source": "local",
	//         "defined_at": "app"
	//       }
	//     ],
	//     "commands": [
	//       {
	//         "name": "serve",
	//         "path": "app serve",
	//         "aliases": [
	//           "s"
	//         ],
	//         "use": "serve",
	//         "runnable": true,
	//         "flags": [
	//           {
	//             "name": "port",
	//             "usage": "port to listen on",
	//             "type": "int",
	//             "default": "8080",
	//             "current_value": "unknown",
	//             "required": false,
	//             "source": "local",
	//             "defined_at": "app serve"
	//           },
	//           {
	//             "name": "region",
	//             "usage": "region to use",
	//             "type": "string",
	//             "default": "us",
	//             "current_value": "unknown",
	//             "required": false,
	//             "source": "inherited",
	//             "defined_at": "app"
	//           }
	//         ]
	//       }
	//     ]
	//   }
	// }
}

func ExampleCommand_ExportContract_invalidGroupMember() {
	root := &Command{Use: "app"}
	sub := &Command{Use: "sub", Run: func(*Command, []string) {}}
	sub.Flags().String("token", "", "auth token")
	// Simulate a flag group annotation referencing a flag that does not exist.
	_ = sub.Flags().SetAnnotation("token", requiredAsGroupAnnotation, []string{"token missing"})
	root.AddCommand(sub)

	_, err := root.ExportContract(nil)
	fmt.Println(err)
	// Output: cobra: command "app sub" has a required_together flag group "token missing" referencing unknown flag "missing"
}
