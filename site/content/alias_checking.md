# Static alias conflict checking

Applications that assemble a command tree from multiple packages can
accidentally register colliding names: one package adds `sync` with
alias `s` while another adds a command literally named `s`. Cobra
resolves such collisions silently at runtime (the first match wins), so
they are best caught statically, right after the tree is registered.

`CheckAliasConflicts` walks a fully registered command tree and reports
**all** collisions at once, without executing any command:

```go
root := &cobra.Command{Use: "root"}
// ... commands added by several packages ...
root.AddCommand(syncCmd, statusCmd, toolsCmd)

conflicts := cobra.CheckAliasConflicts(root)
if len(conflicts) > 0 {
    fmt.Fprintln(os.Stderr, cobra.AliasConflictReport(conflicts))
    os.Exit(1)
}
```

There is also a method form, `root.CheckAliasConflicts()`, which is
equivalent to calling the package-level function with `root`.

## What is checked

The check is scoped per **parent command**. Within one group of sibling
commands, every token a child answers to is compared against the tokens
of its siblings:

- the real name (first word of `Use`),
- every entry of `Aliases`,
- every entry of `SuggestFor` (the configured suggestion aliases).

Identical tokens under *different* parents are **not** a conflict: with
`root sync` (alias `s`) and `root s` at the top level plus `root tools s`
nested below `tools`, only the top-level pair is reported, because
command resolution never compares tokens across parents.

Hidden and deprecated commands participate in the check exactly like
visible ones.

## Conflict kinds

| Kind                    | Meaning                                             |
|-------------------------|-----------------------------------------------------|
| `name-vs-name`          | two siblings share the same real name               |
| `alias-vs-name`         | an alias equals a sibling's real name               |
| `alias-vs-alias`        | two siblings declare the same alias                 |
| `suggest-vs-name`       | a `SuggestFor` entry equals a sibling's real name   |
| `suggest-vs-alias`      | a `SuggestFor` entry equals a sibling's alias       |
| `suggest-vs-suggest`    | two siblings list the same `SuggestFor` entry       |
| `duplicate-declaration` | one command declares the same token twice           |

`duplicate-declaration` is a separate diagnostic for tokens repeated
within a *single* command, e.g. `Aliases: []string{"s", "s"}` or an
alias equal to the command's own name.

Each `AliasConflict` records the kind, the token, both commands, and
both full command paths (`FirstPath`, `SecondPath`), so reports always
name both parties, e.g.:

```text
found 1 command name conflict(s):
  alias-vs-name: token "s" declared by "root sync" and "root s"
```

## Guarantees

- **Static**: the checker never calls `Find` or `Execute`, never
  triggers flag or argument parsing, and never runs `Run`/`RunE`.
- **Read-only**: names, `Aliases`, `SuggestFor`, child ordering and
  called-as state are left untouched (it iterates the registration
  order directly instead of the sorting `Commands()` accessor).
- **Deterministic**: parents are visited depth-first in command
  registration order and declarations are compared in registration
  order, so repeated runs produce identical reports.

## Running the targeted tests

```console
$ go test -run 'TestCheckAliasConflicts' .
```
