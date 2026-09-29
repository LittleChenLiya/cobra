package cobra

import (
	"fmt"
	"strings"
)

// SetAliasMigration sets the table of deprecated command paths mapped to the
// current, canonical command path that replaces them. Both paths are given as
// space separated sequences of command names, for example:
//
//	rootCmd.SetAliasMigration(map[string]string{
//		"old sync": "project pull",
//	})
//
// When an invocation starts with a deprecated path it is rewritten to the
// canonical path and the remaining arguments, including flags and anything
// after "--", are handed to the target command untouched. A single notice
// telling the user about the new command path is printed for every successful
// rewrite, even when several mappings are chained.
//
// Real subcommands always take precedence over a migration: a mapping is only
// considered when no actual command with the same name exists at the matching
// position in the command tree.
func (c *Command) SetAliasMigration(migrations map[string]string) {
	c.aliasMigrations = make(map[string]string, len(migrations))
	for oldPath, newPath := range migrations {
		oldPath = strings.TrimSpace(oldPath)
		if oldPath == "" {
			continue
		}
		c.aliasMigrations[oldPath] = strings.TrimSpace(newPath)
	}
}

// AliasMigration returns the canonical command path configured for the given
// deprecated command path, if any.
func (c *Command) AliasMigration(oldPath string) (newPath string, ok bool) {
	newPath, ok = c.aliasMigrations[oldPath]
	return
}

// applyAliasMigration rewrites a deprecated command prefix in args to the
// canonical command prefix. It returns the rewritten args and whether a
// migration notice should be printed. An error is returned when the migration
// table contains a cycle or points to an empty target.
//
// Matching is performed against the complete command hierarchy: mappings are
// resolved using full command paths, not individual words. The longest
// configured deprecated path that matches the leading command tokens wins.
// If a leading token names an actual subcommand at that level, the real
// subcommand is preferred and no migration happens.
func (c *Command) applyAliasMigration(args []string) ([]string, string, string, error) {
	if len(c.aliasMigrations) == 0 {
		return args, "", "", nil
	}

	oldPath, ok := c.matchAliasMigration(args)
	if !ok {
		return args, "", "", nil
	}

	newPath, err := c.resolveAliasMigration(oldPath, map[string]bool{oldPath: true})
	if err != nil {
		return args, "", "", err
	}
	if c.findCommandPath(strings.Fields(newPath)) == nil {
		return args, "", "", fmt.Errorf("alias migration %q points to unknown command %q", oldPath, newPath)
	}

	newArgs, err := c.rewriteAliasMigrationArgs(args, strings.Fields(oldPath), strings.Fields(newPath))
	if err != nil {
		return args, "", "", err
	}
	return newArgs, oldPath, newPath, nil
}

// matchAliasMigration returns the configured deprecated path that matches the
// leading non-flag command tokens of args. A token that names a real
// subcommand at that level always resolves to the subcommand and prevents a
// migration: in particular, a mapping whose first token is a real root
// subcommand is never used.
func (c *Command) matchAliasMigration(args []string) (string, bool) {
	commandTokens := stripFlags(args, c)
	if len(commandTokens) == 0 {
		return "", false
	}

	// A real subcommand at the root level always wins over a migration.
	if c.findNext(commandTokens[0]) != nil {
		return "", false
	}

	path := ""

	for _, token := range commandTokens {
		if path == "" {
			path = token
		} else {
			path += " " + token
		}

		if _, ok := c.aliasMigrations[path]; ok {
			return path, true
		}
		// Continue walking only while the accumulated path is a prefix of a
		// mapping with more hierarchy levels.
		if !migrationPathPrefix(c.aliasMigrations, path) {
			break
		}
	}
	return "", false
}

func migrationPathPrefix(migrations map[string]string, prefix string) bool {
	for oldPath := range migrations {
		if oldPath != prefix && strings.HasPrefix(oldPath, prefix+" ") {
			return true
		}
	}
	return false
}

// findCommandPath walks the real command tree following tokens and returns the
// resolved command, or nil if any token does not name a subcommand.
func (c *Command) findCommandPath(tokens []string) *Command {
	current := c
	for _, token := range tokens {
		next := current.findNext(token)
		if next == nil {
			return nil
		}
		current = next
	}
	return current
}

// resolveAliasMigration follows chained mappings until a canonical path is
// reached. It reports an error when a cycle is detected or a configured
// target is missing or empty.
func (c *Command) resolveAliasMigration(path string, seen map[string]bool) (string, error) {
	for {
		next, ok := c.aliasMigrations[path]
		if !ok {
			return path, nil
		}
		if strings.TrimSpace(next) == "" {
			return "", fmt.Errorf("alias migration %q has no target command", path)
		}
		if seen[next] {
			return "", fmt.Errorf("alias migration cycle detected: %q eventually maps back to itself", next)
		}
		seen[next] = true
		path = next
	}
}

// rewriteAliasMigrationArgs removes the leading command tokens of the matched
// deprecated path from args and prepends the canonical command tokens. The
// tokens to remove are located with stripFlags so that flags and their values
// are never mistaken for command names. Everything else, including flags and
// anything after "--", is left in place and handed to the target command
// verbatim.
func (c *Command) rewriteAliasMigrationArgs(args, oldTokens, newTokens []string) ([]string, error) {
	if len(oldTokens) == 0 {
		return args, nil
	}

	c.mergePersistentFlags()
	flags := c.Flags()

	remove := make(map[int]bool, len(oldTokens))
	tokenIdx := 0
	for i := 0; i < len(args) && tokenIdx < len(oldTokens); i++ {
		s := args[i]
		switch {
		case s == "--":
			// Command tokens cannot appear after "--".
			return args, nil
		case strings.HasPrefix(s, "--"):
			if !strings.Contains(s, "=") && !hasNoOptDefVal(s[2:], flags) && i+1 < len(args) {
				// Skip the value of "--flag value".
				i++
			}
		case strings.HasPrefix(s, "-") && s != "-":
			if len(s) == 2 && !shortHasNoOptDefVal(s[1:], flags) && i+1 < len(args) {
				i++
			}
		default:
			if s != oldTokens[tokenIdx] {
				return args, nil
			}
			remove[i] = true
			tokenIdx++
		}
	}
	if tokenIdx != len(oldTokens) {
		return args, nil
	}

	result := make([]string, 0, len(newTokens)+len(args)-len(oldTokens))
	result = append(result, newTokens...)
	for i, arg := range args {
		if !remove[i] {
			result = append(result, arg)
		}
	}
	return result, nil
}
