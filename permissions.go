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
	"errors"
	"sort"
	"strconv"
	"strings"
)

// Annotations used to declare the explicit permission plan of a command.
//
// PermissionRequiredAnnotation lists the permissions required to run a
// command. The value is a comma separated list, e.g. "read,write".
//
// Requirements declared on a parent command are inherited by every
// descendant: the effective plan of a command is the union of the
// requirements collected from the root command down to the command itself.
// A child command can therefore only add requirements; it can never remove a
// requirement introduced by one of its parents.
//
// PermissionExemptAnnotation marks a command as a read-only display command
// (help and shell completion for example). Commands carrying this annotation
// are not subjected to the business permission check and no
// PermissionChecker is invoked for them. The annotation is not inherited:
// only the annotated command itself is exempt.
const (
	PermissionRequiredAnnotation = "cobra_annotation_permission_required"
	PermissionExemptAnnotation   = "cobra_annotation_permission_exempt"

	// readOnlyHooksAnnotation is an internal marker for built-in read-only
	// display commands (the "help" command). Unlike PermissionExemptAnnotation
	// (which is also used by shell completion and deliberately keeps
	// PersistentPreRun compatibility) it additionally suppresses business run
	// hooks inherited from ancestors, so the help path triggers no business
	// callback at all.
	readOnlyHooksAnnotation = "cobra_annotation_permission_read_only_hooks"
)

// PermissionPlan is the effective permission plan computed from the root of
// the command tree down to a target command.
//
// Identity is the canonical command path (see CommandPath). A command and all
// of its aliases resolve to the same *Command and therefore share the exact
// same plan.
type PermissionPlan struct {
	// Identity is the canonical, alias independent path of the target command.
	Identity string
	// Required holds every permission required to execute the target command,
	// collected from the root down to the target, in declaration order with
	// duplicates removed.
	Required []string
	// Exempt reports that the target command is a read-only display command
	// for which no business permission check must be performed.
	Exempt bool
}

// PermissionChecker decides whether the caller is allowed to run a command.
//
// Implementations must treat the plan as the full description of what is
// required and return the subset of plan.Required that the caller is missing.
// The checker is not given, and must not leak, the caller's complete
// permission set: only the names of missing requirements are reported back
// through the returned slice and errors.
//
// A nil or empty missing slice means the plan is approved and command
// execution proceeds to PersistentPreRun/PreRun and Run.
type PermissionChecker interface {
	CheckPermissions(plan PermissionPlan) (missing []string, err error)
}

// PermissionCheckerFunc adapts a plain function to a PermissionChecker.
type PermissionCheckerFunc func(plan PermissionPlan) (missing []string, err error)

// CheckPermissions calls f(plan).
func (f PermissionCheckerFunc) CheckPermissions(plan PermissionPlan) ([]string, error) {
	return f(plan)
}

// PermissionDeniedError is returned when a PermissionChecker rejects a plan.
// It only exposes the names of the missing permissions; it never exposes the
// full set of permissions the caller holds.
type PermissionDeniedError struct {
	// Identity is the canonical command path that was rejected.
	Identity string
	// Missing lists the required permissions the caller does not hold.
	Missing []string
}

func (e *PermissionDeniedError) Error() string {
	missing := append([]string(nil), e.Missing...)
	sort.Strings(missing)
	return "permission denied for command " + strconv.Quote(e.Identity) +
		": missing permission(s): " + strings.Join(missing, ", ")
}

// parsePermissionAnnotation reads the comma separated permission list stored
// under PermissionRequiredAnnotation. Blank entries are ignored.
func parsePermissionAnnotation(cmd *Command) []string {
	value, ok := cmd.Annotations[PermissionRequiredAnnotation]
	if !ok || strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	perms := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			perms = append(perms, p)
		}
	}
	return perms
}

// PermissionExempt reports whether the command opts out of the business
// permission check. The built-in help and shell completion commands are
// marked this way; the annotation is not inherited by child commands.
func (c *Command) PermissionExempt() bool {
	if c == nil {
		return false
	}
	switch v := strings.ToLower(strings.TrimSpace(c.Annotations[PermissionExemptAnnotation])); v {
	case "", "false", "0", "no":
		return false
	default:
		return true
	}
}

// EffectivePermissions computes the effective permission plan for c by walking
// the command tree from the root down to c and collecting every declared
// requirement. Parent requirements are always inherited and children can only
// add to the set. Because aliases resolve to the same *Command, calling a
// command through any of its aliases produces the same plan.
func (c *Command) EffectivePermissions() PermissionPlan {
	plan := PermissionPlan{Identity: c.CommandPath(), Exempt: c.PermissionExempt()}

	// Collect from root to target so parent (inherited) requirements keep
	// precedence/order; children can only append new entries.
	chain := make([]*Command, 0, 4)
	for p := c; p != nil; p = p.Parent() {
		chain = append([]*Command{p}, chain...)
	}

	seen := make(map[string]struct{})
	for _, cmd := range chain {
		for _, perm := range parsePermissionAnnotation(cmd) {
			if _, dup := seen[perm]; dup {
				continue
			}
			seen[perm] = struct{}{}
			plan.Required = append(plan.Required, perm)
		}
	}
	return plan
}

// permissionChecker resolves the checker configured on the command or on the
// closest ancestor. A nil result means no checker was registered, in which
// case Cobra preserves its historical behavior and does not gate execution.
func (c *Command) permissionChecker() PermissionChecker {
	for p := c; p != nil; p = p.Parent() {
		if p.permissionCheckerFn != nil {
			return p.permissionCheckerFn
		}
	}
	return nil
}

// SetPermissionChecker registers the checker used to approve the effective
// permission plan of c and its descendants before any business run hook
// (PersistentPreRun, PreRun or Run) is executed.
//
// Set the checker on the root command to enforce one policy for the whole
// tree. Passing nil removes a checker previously set on c (an ancestor's
// checker, if any, still applies).
func (c *Command) SetPermissionChecker(checker PermissionChecker) {
	c.permissionCheckerFn = checker
}

// SetPermissionCheckerFunc is a convenience wrapper around
// SetPermissionChecker accepting a plain function. Passing a nil function
// removes a checker previously set on c.
func (c *Command) SetPermissionCheckerFunc(f func(plan PermissionPlan) (missing []string, err error)) {
	if f == nil {
		c.permissionCheckerFn = nil
		return
	}
	c.permissionCheckerFn = PermissionCheckerFunc(f)
}

// checkPermissions computes the effective plan and asks the resolved checker
// for approval. Exempt, read-only display commands are never checked and no
// business permission action is performed for them. The returned error only
// ever mentions the missing required permissions.
func (c *Command) checkPermissions() error {
	plan := c.EffectivePermissions()
	if plan.Exempt {
		return nil
	}
	checker := c.permissionChecker()
	if checker == nil {
		return nil
	}
	missing, err := checker.CheckPermissions(plan)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}

	// Normalize and restrict the report to declared requirements so a buggy
	// checker cannot leak permissions the caller happens to hold.
	known := make(map[string]struct{}, len(plan.Required))
	for _, p := range plan.Required {
		known[p] = struct{}{}
	}
	denied := make([]string, 0, len(missing))
	seen := make(map[string]struct{}, len(missing))
	for _, p := range missing {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := known[p]; !ok {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		denied = append(denied, p)
	}
	if len(denied) == 0 {
		return nil
	}
	return &PermissionDeniedError{Identity: plan.Identity, Missing: denied}
}

// staticPermissionChecker is a small built-in checker holding the granted
// permission set. It keeps the caller's granted set private: only missing
// requirements are ever reported.
type staticPermissionChecker struct {
	granted map[string]struct{}
}

// NewStaticPermissionChecker builds a PermissionChecker that approves a plan
// when every required permission is present in granted. The returned checker
// only reports missing permission names and never the granted set itself.
func NewStaticPermissionChecker(granted ...string) PermissionChecker {
	set := make(map[string]struct{}, len(granted))
	for _, p := range granted {
		if p = strings.TrimSpace(p); p != "" {
			set[p] = struct{}{}
		}
	}
	return staticPermissionChecker{granted: set}
}

// CheckPermissions implements PermissionChecker.
func (s staticPermissionChecker) CheckPermissions(plan PermissionPlan) ([]string, error) {
	var missing []string
	for _, required := range plan.Required {
		if _, ok := s.granted[required]; !ok {
			missing = append(missing, required)
		}
	}
	return missing, nil
}

// AsPermissionDenied returns the PermissionDeniedError wrapped in err, if any.
func AsPermissionDenied(err error) (*PermissionDeniedError, bool) {
	var denied *PermissionDeniedError
	if errors.As(err, &denied) {
		return denied, true
	}
	return nil, false
}

// markPermissionExempt stamps the read-only display exemption annotation on
// the command. It is used for the built-in help and completion machinery.
func (c *Command) markPermissionExempt() {
	if c.Annotations == nil {
		c.Annotations = map[string]string{}
	}
	c.Annotations[PermissionExemptAnnotation] = "true"
}

// suppressBusinessHooks marks c as a read-only display command that must not
// execute business run hooks inherited from its ancestors. It also implies
// PermissionExempt, so no permission check is performed either.
func (c *Command) suppressBusinessHooks() {
	c.markPermissionExempt()
	if c.Annotations == nil {
		c.Annotations = map[string]string{}
	}
	c.Annotations[readOnlyHooksAnnotation] = "true"
}

// businessHooksSuppressed reports whether inherited business run hooks must be
// skipped for this read-only display command.
func (c *Command) businessHooksSuppressed() bool {
	switch v := strings.ToLower(strings.TrimSpace(c.Annotations[readOnlyHooksAnnotation])); v {
	case "", "false", "0", "no":
		return false
	default:
		return true
	}
}
