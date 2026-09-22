# Command Contract Export

Cobra can export a command tree as a versioned, statically-known contract
model, suitable for offline consumption by IDEs, documentation generators and
other tooling that cannot (or must not) execute the binary.

The export is purely static:

- It never invokes `Run`, `PreRun`, `Args` validators, dynamic completion
  functions or `pflag.Value.Set`.
- It does not modify the command tree or any flag state; repeated exports of
  an unmodified tree produce byte-identical JSON.
- Flag inheritance and shadowing follow Cobra's own runtime semantics
  (`LocalFlags` / `InheritedFlags`).
- Content that cannot be determined statically (for example the runtime value
  of a flag) is reported as `"unknown"`.

## Exporting

```go
contract, err := rootCmd.ExportContract(nil)
if err != nil {
    // duplicate alias, command tree cycle, or invalid flag group member;
    // the error always names the canonical command path.
    log.Fatal(err)
}

// Or directly as indented JSON:
data, err := rootCmd.ExportContractJSON(&cobra.ExportOptions{
    IncludeHidden:     true,  // also export hidden commands
    IncludeDeprecated: true,  // also export deprecated commands
})
```

Hidden and deprecated commands are excluded unless the corresponding
`ExportOptions` field is set. Aliases are exported as plain names pointing at
the canonical command; they never duplicate the subcommand tree.

## JSON contract (version 1)

The top-level object is a `Contract`:

```json
{
  "version": 1,
  "root": { "...": "CommandContract" }
}
```

### CommandContract

| Field         | Type              | Description                                             |
|---------------|-------------------|---------------------------------------------------------|
| `name`        | string            | Canonical command name.                                 |
| `path`        | string            | Canonical command path, e.g. `"root child"`.            |
| `aliases`     | []string          | Alternative names resolving to this command.            |
| `use`         | string            | One-line usage, including argument documentation.       |
| `short`       | string            | Short description (omitted when empty).                 |
| `long`        | string            | Long description (omitted when empty).                  |
| `example`     | string            | Help examples (omitted when empty).                     |
| `valid_args`  | []string          | Statically declared positional arguments (omitted when empty). |
| `runnable`    | bool              | Whether the command has a `Run`/`RunE` function.        |
| `hidden`      | bool              | Whether the command is hidden (omitted when false).     |
| `deprecated`  | string            | Deprecation message (omitted when empty).               |
| `flags`       | []FlagContract    | Effective flags, sorted by name (see below).            |
| `flag_groups` | []FlagGroupContract | Flag group constraints applying to this command.      |
| `commands`    | []CommandContract | Exported subcommands, sorted by name.                   |

### FlagContract

`flags` contains the *effective* flags of the command: local flags plus
inherited persistent flags, with a local flag shadowing a same-named inherited
one. Each flag records where it came from so shadowing can be traced:

| Field             | Type   | Description                                          |
|-------------------|--------|------------------------------------------------------|
| `name`            | string | Long flag name.                                      |
| `shorthand`       | string | One-letter shorthand (omitted when empty).           |
| `usage`           | string | Help text.                                           |
| `type`            | string | Static pflag type name, e.g. `"string"`, `"int"`.    |
| `default`         | string | Static default captured when the flag was defined.   |
| `current_value`   | string | Always `"unknown"`; runtime values are never read.   |
| `required`        | bool   | Whether the flag was marked required.                |
| `hidden`          | bool   | Whether the flag is hidden (omitted when false).     |
| `deprecated`      | string | Deprecation message (omitted when empty).            |
| `no_opt_def_val`  | string | Value used when the flag is given without an argument (omitted when empty). |
| `source`          | string | `"local"` or `"inherited"`.                          |
| `defined_at`      | string | Canonical path of the command defining the flag.     |

### FlagGroupContract

Flag groups declared with `MarkFlagsRequiredTogether`, `MarkFlagsOneRequired`
or `MarkFlagsMutuallyExclusive` are exported per command, including groups
declared on inherited persistent flags:

| Field   | Type     | Description                                                        |
|---------|----------|--------------------------------------------------------------------|
| `type`  | string   | `"required_together"`, `"one_required"` or `"mutually_exclusive"`. |
| `flags` | []string | Sorted names of the flags participating in the group.              |

## Errors

Export fails with an error naming the canonical command path when the tree
contains:

- a duplicate alias (or an alias equal to the command's own name),
- a cycle in the parent/child command chain,
- a flag group referencing a flag that does not exist on the command.

## Example

```go
root := &cobra.Command{Use: "app", Short: "demo app"}
root.PersistentFlags().String("region", "us", "region to use")
serve := &cobra.Command{Use: "serve", Aliases: []string{"s"}, Run: func(*cobra.Command, []string) {}}
serve.Flags().Int("port", 8080, "port to listen on")
root.AddCommand(serve)

data, _ := root.ExportContractJSON(nil)
fmt.Println(string(data))
```

```json
{
  "version": 1,
  "root": {
    "name": "app",
    "path": "app",
    "use": "app",
    "short": "demo app",
    "runnable": false,
    "flags": [
      {
        "name": "region",
        "usage": "region to use",
        "type": "string",
        "default": "us",
        "current_value": "unknown",
        "required": false,
        "source": "local",
        "defined_at": "app"
      }
    ],
    "commands": [
      {
        "name": "serve",
        "path": "app serve",
        "aliases": ["s"],
        "use": "serve",
        "runnable": true,
        "flags": [
          {
            "name": "port",
            "usage": "port to listen on",
            "type": "int",
            "default": "8080",
            "current_value": "unknown",
            "required": false,
            "source": "local",
            "defined_at": "app serve"
          },
          {
            "name": "region",
            "usage": "region to use",
            "type": "string",
            "default": "us",
            "current_value": "unknown",
            "required": false,
            "source": "inherited",
            "defined_at": "app"
          }
        ]
      }
    ]
  }
}
```
