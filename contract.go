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
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	flag "github.com/spf13/pflag"
)

// ContractVersion is the version of the command tree contract model
// produced by ExportContract. It is incremented whenever the model
// changes in a backwards-incompatible way.
const ContractVersion = 1

// Flag group kinds exported in FlagGroupContract.Kind.
const (
	// FlagGroupRequiredTogether marks a group whose flags must all be set together.
	FlagGroupRequiredTogether = "required_together"
	// FlagGroupOneRequired marks a group of which at least one flag must be set.
	FlagGroupOneRequired = "one_required"
	// FlagGroupMutuallyExclusive marks a group of which at most one flag may be set.
	FlagGroupMutuallyExclusive = "mutually_exclusive"
)

// Flag sources exported in FlagContract.Source.
const (
	// FlagSourceLocal marks a flag defined on the exported command itself.
	FlagSourceLocal = "local"
	// FlagSourceInherited marks a persistent flag inherited from a parent command.
	FlagSourceInherited = "inherited"
)

// unknownValue is the placeholder used for contract fields whose value can
// only be determined dynamically at runtime and is therefore not statically
// exportable.
const unknownValue = "unknown"

// ExportOptions controls which parts of a command tree are included in the
// exported contract.
type ExportOptions struct {
	// IncludeHidden includes hidden commands in the exported contract.
	IncludeHidden bool
	// IncludeDeprecated includes deprecated commands in the exported contract.
	IncludeDeprecated bool
}

// CommandContract is a versioned, structured model of a command tree,
// suitable for offline consumption by IDEs and documentation tools.
type CommandContract struct {
	// Version is the contract model version, see ContractVersion.
	Version int `json:"version"`
	// Root is the root of the exported command tree.
	Root *CommandNode `json:"root"`
}

// CommandNode describes a single command in an exported command tree.
type CommandNode struct {
	// Name is the canonical name of the command.
	Name string `json:"name"`
	// Path is the canonical command path, e.g. "root child grandchild".
	Path string `json:"path"`
	// Use is the one-line usage message, including argument documentation.
	Use string `json:"use"`
	// Short is the short description of the command.
	Short string `json:"short,omitempty"`
	// Long is the long description of the command.
	Long string `json:"long,omitempty"`
	// Aliases lists the alternative names that resolve to this canonical
	// command. Aliases only reference the canonical identity; the subtree
	// is not duplicated per alias.
	Aliases []string `json:"aliases,omitempty"`
	// ValidArgs lists the statically declared valid positional arguments.
	ValidArgs []string `json:"valid_args,omitempty"`
	// ArgsValidation is "unknown" when the command has a positional argument
	// validator, because validators are dynamic and cannot be exported
	// statically. It is empty when no validator is registered.
	ArgsValidation string `json:"args_validation,omitempty"`
	// Hidden indicates the command is hidden. Hidden commands are only
	// exported when ExportOptions.IncludeHidden is set.
	Hidden bool `json:"hidden,omitempty"`
	// Deprecated holds the deprecation message. Deprecated commands are only
	// exported when ExportOptions.IncludeDeprecated is set.
	Deprecated string `json:"deprecated,omitempty"`
	// Flags lists the flags effective on this command: its local flags
	// (including shadowing ones) and the persistent flags inherited from
	// its parents, following Cobra's own inheritance semantics.
	Flags []FlagContract `json:"flags,omitempty"`
	// FlagGroups lists the flag groups effective on this command.
	FlagGroups []FlagGroupContract `json:"flag_groups,omitempty"`
	// Commands lists the exported subcommands, sorted by name.
	Commands []*CommandNode `json:"commands,omitempty"`
}

// FlagContract describes a single flag effective on a command.
type FlagContract struct {
	// Name is the long name of the flag.
	Name string `json:"name"`
	// Shorthand is the one-letter shorthand, if any.
	Shorthand string `json:"shorthand,omitempty"`
	// Usage is the help text of the flag.
	Usage string `json:"usage,omitempty"`
	// Type is the static value type of the flag, e.g. "string" or "int".
	Type string `json:"type"`
	// Default is the static default value as declared at definition time.
	Default string `json:"default"`
	// Required indicates the flag was marked required.
	Required bool `json:"required,omitempty"`
	// Hidden indicates the flag is hidden from help output.
	Hidden bool `json:"hidden,omitempty"`
	// Deprecated holds the deprecation message of the flag, if any.
	Deprecated string `json:"deprecated,omitempty"`
	// Persistent indicates the flag is persistent and therefore inherited
	// by subcommands.
	Persistent bool `json:"persistent,omitempty"`
	// Source is FlagSourceLocal or FlagSourceInherited.
	Source string `json:"source"`
	// DefinedBy is the canonical path of the command on which the flag
	// was defined.
	DefinedBy string `json:"defined_by"`
	// Shadows is the canonical path of the nearest ancestor command whose
	// persistent flag of the same name is shadowed by this local flag.
	// It is empty when the flag does not shadow anything.
	Shadows string `json:"shadows,omitempty"`
}

// FlagGroupContract describes a relationship between a set of flags.
type FlagGroupContract struct {
	// Kind is one of FlagGroupRequiredTogether, FlagGroupOneRequired or
	// FlagGroupMutuallyExclusive.
	Kind string `json:"kind"`
	// Flags lists the member flag names of the group.
	Flags []string `json:"flags"`
}

// ExportContract exports the command tree rooted at c as a versioned,
// structured contract model. The export is purely static: it never invokes
// Run/PreRun hooks, Args validators, dynamic completion functions or
// flag Value.Set, and it does not modify the command tree or flag state,
// so repeated exports of the same tree are byte-identical.
//
// Flag inheritance and shadowing follow Cobra's own semantics by reusing
// LocalFlags and InheritedFlags. Dynamic content that cannot be determined
// statically is reported as "unknown".
//
// An error is returned, including the offending command path, when the tree
// contains duplicate aliases, a parent/child command cycle, or a flag group
// referencing an undefined flag.
func (c *Command) ExportContract(opts *ExportOptions) (*CommandContract, error) {
	e := contractExporter{}
	if opts != nil {
		e.opts = *opts
	}
	root, err := e.exportCommand(c, c.Name(), map[*Command]bool{})
	if err != nil {
		return nil, err
	}
	return &CommandContract{Version: ContractVersion, Root: root}, nil
}

// WriteContract exports the command tree rooted at c and writes it to w as
// indented JSON. See ExportContract for the export semantics.
func (c *Command) WriteContract(w io.Writer, opts *ExportOptions) error {
	contract, err := c.ExportContract(opts)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(contract, "", "  ")
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n"))
	return err
}

type contractExporter struct {
	opts ExportOptions
}

// exportCommand exports c and, recursively, its subcommands. path is the
// canonical command path of c and ancestors holds the commands on the path
// from the export root to c, for cycle detection.
func (e *contractExporter) exportCommand(c *Command, path string, ancestors map[*Command]bool) (*CommandNode, error) {
	if ancestors[c] {
		return nil, fmt.Errorf("cobra: contract export: command cycle detected at %q", path)
	}
	ancestors[c] = true
	defer delete(ancestors, c)

	// Verify the parent chain is cycle-free before any flag set merging,
	// because merging walks the parents and would not terminate on a cycle.
	if err := checkParentChain(c, path); err != nil {
		return nil, err
	}

	node := &CommandNode{
		Name: c.Name(),
		Path: path,
		Use:  c.Use,
	}
	if c.Short != "" {
		node.Short = c.Short
	}
	if c.Long != "" {
		node.Long = c.Long
	}
	if len(c.Aliases) > 0 {
		seen := make(map[string]bool, len(c.Aliases))
		for _, alias := range c.Aliases {
			if seen[alias] {
				return nil, fmt.Errorf("cobra: contract export: command %q has duplicate alias %q", path, alias)
			}
			seen[alias] = true
		}
		node.Aliases = append([]string(nil), c.Aliases...)
	}
	if len(c.ValidArgs) > 0 {
		node.ValidArgs = append([]string(nil), c.ValidArgs...)
	}
	if c.Args != nil {
		// Positional argument validators are dynamic and cannot be
		// exported statically.
		node.ArgsValidation = unknownValue
	}
	node.Hidden = c.Hidden
	node.Deprecated = c.Deprecated

	flags, err := e.exportFlags(c, path)
	if err != nil {
		return nil, err
	}
	node.Flags = flags
	groups, err := exportFlagGroups(c, path)
	if err != nil {
		return nil, err
	}
	node.FlagGroups = groups

	children := make([]*Command, 0, len(c.Commands()))
	for _, child := range c.Commands() {
		if child.Hidden && !e.opts.IncludeHidden {
			continue
		}
		if child.Deprecated != "" && !e.opts.IncludeDeprecated {
			continue
		}
		children = append(children, child)
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
	for _, child := range children {
		childNode, err := e.exportCommand(child, path+" "+child.Name(), ancestors)
		if err != nil {
			return nil, err
		}
		node.Commands = append(node.Commands, childNode)
	}
	return node, nil
}

// checkParentChain walks the parents of c and reports an error, including
// the command path of c, if a cycle is found.
func checkParentChain(c *Command, path string) error {
	seen := map[*Command]bool{c: true}
	for p := c.Parent(); p != nil; p = p.Parent() {
		if seen[p] {
			return fmt.Errorf("cobra: contract export: command cycle detected in the parent chain of %q", path)
		}
		seen[p] = true
	}
	return nil
}

// exportFlags returns the effective flags of c, sorted by name: local flags
// (including flags shadowing an inherited persistent flag) and inherited
// persistent flags, following Cobra's own inheritance semantics.
func (e *contractExporter) exportFlags(c *Command, path string) ([]FlagContract, error) {
	var flags []FlagContract

	c.LocalFlags().VisitAll(func(f *flag.Flag) {
		fc := newFlagContract(f)
		fc.Source = FlagSourceLocal
		fc.Persistent = c.PersistentFlags().Lookup(f.Name) != nil
		fc.DefinedBy = path
		if shadowed := findInheritedFlag(c, f.Name); shadowed != "" {
			fc.Shadows = shadowed
		}
		flags = append(flags, fc)
	})
	c.InheritedFlags().VisitAll(func(f *flag.Flag) {
		fc := newFlagContract(f)
		fc.Source = FlagSourceInherited
		fc.Persistent = true
		fc.DefinedBy = findInheritedFlag(c, f.Name)
		flags = append(flags, fc)
	})

	sort.Slice(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })
	return flags, nil
}

// newFlagContract builds the static part of a flag contract. It only reads
// metadata captured at definition time and never invokes Value.Set.
func newFlagContract(f *flag.Flag) FlagContract {
	fc := FlagContract{
		Name:       f.Name,
		Shorthand:  f.Shorthand,
		Usage:      f.Usage,
		Default:    f.DefValue,
		Hidden:     f.Hidden,
		Deprecated: f.Deprecated,
	}
	if f.Value != nil {
		fc.Type = f.Value.Type()
	}
	if fc.Type == "" {
		fc.Type = unknownValue
	}
	if required := f.Annotations[BashCompOneRequiredFlag]; len(required) > 0 && required[0] == "true" {
		fc.Required = true
	}
	return fc
}

// findInheritedFlag returns the canonical path of the nearest ancestor of c
// that defines a persistent flag with the given name, or "" if there is none.
// The caller must have verified that the parent chain is cycle-free.
func findInheritedFlag(c *Command, name string) string {
	for p := c.Parent(); p != nil; p = p.Parent() {
		if p.PersistentFlags().Lookup(name) != nil {
			return p.CommandPath()
		}
	}
	return ""
}

// exportFlagGroups returns the flag groups effective on c, sorted for stable
// output. Group members are resolved against the effective flags of c, so
// groups declared on a parent command apply to its children as long as all
// member flags are inherited. A group referencing an undefined flag is an
// error that includes the command path.
func exportFlagGroups(c *Command, path string) ([]FlagGroupContract, error) {
	effective := c.Flags()
	kinds := []struct {
		kind       string
		annotation string
	}{
		{FlagGroupRequiredTogether, requiredAsGroupAnnotation},
		{FlagGroupOneRequired, oneRequiredAnnotation},
		{FlagGroupMutuallyExclusive, mutuallyExclusiveAnnotation},
	}

	seen := make(map[string]bool)
	var groups []FlagGroupContract
	collect := func(f *flag.Flag) error {
		for _, k := range kinds {
			for _, group := range f.Annotations[k.annotation] {
				key := k.kind + "\x00" + group
				if seen[key] {
					continue
				}
				seen[key] = true
				members := strings.Split(group, " ")
				for _, member := range members {
					if effective.Lookup(member) == nil {
						return fmt.Errorf("cobra: contract export: command %q has a %s flag group [%s] referencing undefined flag %q",
							path, k.kind, group, member)
					}
				}
				groups = append(groups, FlagGroupContract{Kind: k.kind, Flags: members})
			}
		}
		return nil
	}

	var err error
	visit := func(f *flag.Flag) {
		if err == nil {
			err = collect(f)
		}
	}
	c.LocalFlags().VisitAll(visit)
	c.InheritedFlags().VisitAll(visit)
	if err != nil {
		return nil, err
	}

	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Kind != groups[j].Kind {
			return groups[i].Kind < groups[j].Kind
		}
		return strings.Join(groups[i].Flags, " ") < strings.Join(groups[j].Flags, " ")
	})
	return groups, nil
}
