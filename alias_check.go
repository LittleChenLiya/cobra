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

// AliasConflictKind describes how two command names collide.
type AliasConflictKind string

const (
	// ConflictNameVsName is reported when two sibling commands share the
	// same real name (the first word of Use).
	ConflictNameVsName AliasConflictKind = "name-vs-name"
	// ConflictAliasVsName is reported when an alias of one command equals
	// the real name of a sibling command.
	ConflictAliasVsName AliasConflictKind = "alias-vs-name"
	// ConflictAliasVsAlias is reported when two sibling commands declare
	// the same alias.
	ConflictAliasVsAlias AliasConflictKind = "alias-vs-alias"
	// ConflictSuggestVsName is reported when a SuggestFor entry of one
	// command equals the real name of a sibling command.
	ConflictSuggestVsName AliasConflictKind = "suggest-vs-name"
	// ConflictSuggestVsAlias is reported when a SuggestFor entry of one
	// command equals an alias of a sibling command.
	ConflictSuggestVsAlias AliasConflictKind = "suggest-vs-alias"
	// ConflictSuggestVsSuggest is reported when two sibling commands list
	// the same SuggestFor entry.
	ConflictSuggestVsSuggest AliasConflictKind = "suggest-vs-suggest"
	// ConflictDuplicateDeclaration is reported when a single command
	// declares the same token more than once: an alias repeated in
	// Aliases, an alias equal to the command name itself, or a
	// SuggestFor entry repeated or equal to the name or an alias.
	ConflictDuplicateDeclaration AliasConflictKind = "duplicate-declaration"
)

// AliasConflict describes a single static name collision found by
// CheckAliasConflicts. First and Second are the two commands involved;
// for ConflictDuplicateDeclaration they are the same command.
type AliasConflict struct {
	// Kind classifies the collision.
	Kind AliasConflictKind
	// Token is the conflicting name, alias or SuggestFor entry.
	Token string
	// First is the command that registered the token first, in
	// registration order.
	First *Command
	// Second is the command whose declaration collides with First.
	Second *Command
	// FirstPath is the full command path of First (e.g. "root sync").
	FirstPath string
	// SecondPath is the full command path of Second (e.g. "root s").
	SecondPath string
}

// String returns a human readable, single line description of the
// conflict, including the full paths of both commands involved.
func (ac AliasConflict) String() string {
	return fmt.Sprintf("%s: token %q declared by %q and %q",
		ac.Kind, ac.Token, ac.FirstPath, ac.SecondPath)
}

// nameSource identifies which declaration of a command produced a token.
type nameSource int

const (
	sourceName nameSource = iota
	sourceAlias
	sourceSuggest
)

// nameEntry is one token a command answers to within its parent scope.
type nameEntry struct {
	token  string
	source nameSource
	cmd    *Command
}

// conflictKindFor maps a pair of declaration sources to a conflict kind.
func conflictKindFor(a, b nameSource) AliasConflictKind {
	// Normalize so that a <= b for a stable lookup.
	if a > b {
		a, b = b, a
	}
	switch {
	case a == sourceName && b == sourceName:
		return ConflictNameVsName
	case a == sourceName && b == sourceAlias:
		return ConflictAliasVsName
	case a == sourceAlias && b == sourceAlias:
		return ConflictAliasVsAlias
	case a == sourceName && b == sourceSuggest:
		return ConflictSuggestVsName
	case a == sourceAlias && b == sourceSuggest:
		return ConflictSuggestVsAlias
	default:
		return ConflictSuggestVsSuggest
	}
}

// CheckAliasConflicts statically checks the command tree rooted at root
// for name collisions and reports every conflict at once, without
// executing any command.
//
// The check is scoped per parent command: within one group of sibling
// commands the real names (first word of Use), the declared Aliases and
// the configured SuggestFor entries of every child are compared against
// each other. Identical tokens used under different parents (for example
// "root s" and "root tools s") do not conflict, because command
// resolution never compares them.
//
// Hidden and deprecated commands participate in the check exactly like
// visible ones. The check is purely static and read-only: it never calls
// Find or Execute, never triggers flag or argument parsing, and never
// modifies the command tree (names, aliases, ordering and called-as
// state are all left untouched).
//
// The returned conflicts are ordered deterministically: parents are
// visited depth-first in command registration order, and within each
// parent the declarations are compared in registration order. A token
// declared twice by the same command yields a separate
// ConflictDuplicateDeclaration diagnostic.
func CheckAliasConflicts(root *Command) []AliasConflict {
	if root == nil {
		return nil
	}
	var conflicts []AliasConflict
	var walk func(c *Command)
	walk = func(c *Command) {
		conflicts = append(conflicts, checkSiblingAliases(c)...)
		// Iterate c.commands directly instead of Commands() so the
		// check stays read-only (Commands() may sort in place) and the
		// report order follows registration order.
		for _, child := range c.commands {
			walk(child)
		}
	}
	walk(root)
	return conflicts
}

// CheckAliasConflicts statically checks the command tree rooted at this
// command for name collisions. See the package level CheckAliasConflicts
// function for details.
func (c *Command) CheckAliasConflicts() []AliasConflict {
	return CheckAliasConflicts(c)
}

// checkSiblingAliases compares the declared tokens of all direct
// children of parent against each other, in registration order.
func checkSiblingAliases(parent *Command) []AliasConflict {
	var conflicts []AliasConflict
	var entries []nameEntry

	for _, child := range parent.commands {
		own := commandNameEntries(child)
		// First, report tokens declared twice by the same command as
		// their own diagnostic.
		conflicts = append(conflicts, duplicateDeclarations(child, own)...)
		// Then compare every new entry against everything registered
		// before it, preserving registration order in the report.
		for _, e := range own {
			for _, prev := range entries {
				if commandNameMatches(prev.token, e.token) {
					conflicts = append(conflicts, AliasConflict{
						Kind:       conflictKindFor(prev.source, e.source),
						Token:      e.token,
						First:      prev.cmd,
						Second:     e.cmd,
						FirstPath:  prev.cmd.CommandPath(),
						SecondPath: e.cmd.CommandPath(),
					})
				}
			}
		}
		entries = append(entries, own...)
	}
	return conflicts
}

// commandNameEntries returns every token c answers to within its parent
// scope: its real name, then its aliases, then its SuggestFor entries,
// each in declaration order.
func commandNameEntries(c *Command) []nameEntry {
	entries := make([]nameEntry, 0, 1+len(c.Aliases)+len(c.SuggestFor))
	entries = append(entries, nameEntry{token: c.Name(), source: sourceName, cmd: c})
	for _, a := range c.Aliases {
		entries = append(entries, nameEntry{token: a, source: sourceAlias, cmd: c})
	}
	for _, s := range c.SuggestFor {
		entries = append(entries, nameEntry{token: s, source: sourceSuggest, cmd: c})
	}
	return entries
}

// duplicateDeclarations reports every token that c declares more than
// once across its name, aliases and SuggestFor entries.
func duplicateDeclarations(c *Command, entries []nameEntry) []AliasConflict {
	var conflicts []AliasConflict
	for i := 1; i < len(entries); i++ {
		for j := 0; j < i; j++ {
			if commandNameMatches(entries[j].token, entries[i].token) {
				path := c.CommandPath()
				conflicts = append(conflicts, AliasConflict{
					Kind:       ConflictDuplicateDeclaration,
					Token:      entries[i].token,
					First:      c,
					Second:     c,
					FirstPath:  path,
					SecondPath: path,
				})
				break
			}
		}
	}
	return conflicts
}

// AliasConflictReport renders conflicts as a deterministic, human
// readable report, one conflict per line, in the order given. It returns
// an empty string when there are no conflicts.
func AliasConflictReport(conflicts []AliasConflict) string {
	if len(conflicts) == 0 {
		return ""
	}
	lines := make([]string, 0, len(conflicts)+1)
	lines = append(lines, fmt.Sprintf("found %d command name conflict(s):", len(conflicts)))
	for _, c := range conflicts {
		lines = append(lines, "  "+c.String())
	}
	return strings.Join(lines, "\n")
}
