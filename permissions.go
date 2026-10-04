// Copyright 2013-2024 The Cobra Authors
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

// PermissionsAnnotation is the command annotation used to declare the
// permissions required to execute a command. The value is a comma-separated
// list of permission names, e.g. "read,write". Whitespace around names is
// ignored and duplicate names are collapsed.
//
// Permissions declared on a command are inherited by every descendant: the
// effective plan for a command is the union of the permissions declared on
// all commands from the root of the tree down to the target command. A child
// can only add permissions to the plan; it can never remove one declared by
// an ancestor.
const PermissionsAnnotation = "cobra.permissions"

// PermissionsSkipAnnotation marks a command as a read-only entry point that
// must not trigger permission checks. Cobra sets it on the built-in help and
// completion commands. Applications may set it on their own read-only
// commands as well.
const PermissionsSkipAnnotation = "cobra.skip-permissions"

// PermissionChecker is implemented by applications that gate command
// execution on permissions. Cobra calls GrantedPermissions with the effective
// permission plan (the union of all permissions required from the root command
// down to the command being executed) and expects the subset of permissions
// the caller actually holds.
//
// Implementations must only return permissions that appear in the required
// slice; returning permissions that were not requested has no effect.
type PermissionChecker interface {
	GrantedPermissions(required []string) []string
}

// PermissionCheckerFunc adapts a function to the PermissionChecker interface.
type PermissionCheckerFunc func(required []string) []string

// GrantedPermissions implements PermissionChecker.
func (f PermissionCheckerFunc) GrantedPermissions(required []string) []string {
	return f(required)
}

// MissingPermissionsError is returned when the caller lacks one or more of the
// permissions required to execute a command. It only exposes the names of the
// missing permissions; it never reveals which permissions the caller holds.
type MissingPermissionsError struct {
	Missing []string
}

func (e *MissingPermissionsError) Error() string {
	return fmt.Sprintf("missing required permission(s): %s", strings.Join(e.Missing, ", "))
}

// RequiredPermissions returns the permissions declared directly on c via the
// PermissionsAnnotation, in declaration order with duplicates removed.
func (c *Command) RequiredPermissions() []string {
	return splitPermissions(c.Annotations[PermissionsAnnotation])
}

// PermissionPlan returns the effective permissions required to execute c:
// the union of the permissions declared on every command from the root of the
// tree down to c.
//
// Permissions declared by ancestors are inherited by default; a command can
// only add to the plan, never remove from it. Invoking a command through an
// alias resolves to the same Command as its canonical name, so both produce
// the exact same plan.
func (c *Command) PermissionPlan() []string {
	chain := make([]*Command, 0, 4)
	for p := c; p != nil; p = p.Parent() {
		chain = append([]*Command{p}, chain...)
	}

	plan := make([]string, 0)
	seen := make(map[string]struct{})
	for _, cmd := range chain {
		for _, perm := range cmd.RequiredPermissions() {
			if _, ok := seen[perm]; ok {
				continue
			}
			seen[perm] = struct{}{}
			plan = append(plan, perm)
		}
	}
	return plan
}

// HasPermission reports whether the effective plan for c contains perm.
func (c *Command) HasPermission(perm string) bool {
	for _, p := range c.PermissionPlan() {
		if p == perm {
			return true
		}
	}
	return false
}

// checkPermissions runs the permission layer before a command is executed.
// It returns nil when no checker is configured, when the command is marked as
// read-only (PermissionsSkipAnnotation), or when the caller holds every
// permission in the effective plan. Otherwise it returns a
// MissingPermissionsError listing only the names of the missing permissions.
func (c *Command) checkPermissions() error {
	checker := c.permissionChecker()
	if checker == nil {
		return nil
	}
	if _, skip := c.Annotations[PermissionsSkipAnnotation]; skip {
		return nil
	}

	required := c.PermissionPlan()
	if len(required) == 0 {
		return nil
	}

	granted := checker.GrantedPermissions(required)
	held := make(map[string]struct{}, len(granted))
	for _, p := range granted {
		held[p] = struct{}{}
	}

	missing := make([]string, 0)
	for _, p := range required {
		if _, ok := held[p]; !ok {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &MissingPermissionsError{Missing: missing}
}

// permissionChecker returns the checker configured on the root command, if
// any. The checker is always read from the root so that it applies uniformly
// to the whole tree.
func (c *Command) permissionChecker() PermissionChecker {
	if root := c.Root(); root != nil {
		return root.PermissionChecker
	}
	return nil
}

// skipsPermissionChecks reports whether c is a read-only command (marked with
// PermissionsSkipAnnotation) for which the permission layer and inherited
// business callbacks must not run.
func (c *Command) skipsPermissionChecks() bool {
	_, skip := c.Annotations[PermissionsSkipAnnotation]
	return skip
}

// splitPermissions parses a comma-separated permission list, trimming
// whitespace and dropping empty entries and duplicates.
func splitPermissions(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{})
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
