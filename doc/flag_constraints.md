# Flag Constraints

Cobra's flag groups (`MarkFlagsRequiredTogether`, `MarkFlagsOneRequired`,
`MarkFlagsMutuallyExclusive`) cover the common cases. When you need
conditional relationships such as "selecting remote mode requires an endpoint
address, and a local file must not be given at the same time", you can
register composable flag constraint expressions with `AddFlagConstraint`.

## Expressions

A constraint is a boolean expression over flag presence. A flag is *present*
when it was explicitly set on the command line (pflag's `Changed` state), so
`--enabled=false` counts as present while an untouched default does not.

| Constructor                | Meaning                                          |
|----------------------------|--------------------------------------------------|
| `Present("flag")`          | the flag was explicitly set                      |
| `All(e1, e2, ...)`         | all sub-expressions hold (at least one required) |
| `Any(e1, e2, ...)`         | at least one sub-expression holds (one required) |
| `Not(e)`                   | the sub-expression does not hold                 |
| `Implies(cond, then)`      | whenever `cond` holds, `then` must hold          |
| `Ref("rule-id")`           | reuses the expression of another registered rule |

Rules are registered per command and validated before `Run` executes:

```go
syncCmd.Flags().Bool("remote", false, "use remote mode")
syncCmd.Flags().String("endpoint", "", "remote endpoint address")
syncCmd.Flags().String("file", "", "local input file")

_ = syncCmd.AddFlagConstraint("remote-requires-endpoint",
	cobra.Implies(cobra.Present("remote"), cobra.Present("endpoint")))
_ = syncCmd.AddFlagConstraint("remote-excludes-file",
	cobra.Implies(cobra.Present("remote"), cobra.Not(cobra.Present("file"))))
```

Flag names are bound when the command executes: a local flag shadows an
inherited persistent flag of the same name, otherwise the name binds to the
inherited flag. Unknown flags, unknown or circular `Ref` references, and
empty `All`/`Any` expressions are reported as errors. Violations are
reported with the rule ID and the bound flag names, in rule declaration
order. The size of the constraint set is bounded by
`MaxFlagConstraintExprSize`, `MaxFlagConstraintRules` and
`MaxFlagConstraintFlags`; registration rejects rules that exceed them.

Shell completion filters out only the flags that could never lead to a valid
combination (flags not yet passed may still be supplied later); every other
candidate keeps its usual order.

## Try it locally in 90 seconds

Save the following as `main.go` in an empty directory:

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{Use: "demo"}
	sync := &cobra.Command{
		Use: "sync",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("syncing...")
		},
	}
	sync.Flags().Bool("remote", false, "use remote mode")
	sync.Flags().String("endpoint", "", "remote endpoint address")
	sync.Flags().String("file", "", "local input file")
	_ = sync.AddFlagConstraint("remote-requires-endpoint",
		cobra.Implies(cobra.Present("remote"), cobra.Present("endpoint")))
	_ = sync.AddFlagConstraint("remote-excludes-file",
		cobra.Implies(cobra.Present("remote"), cobra.Not(cobra.Present("file"))))
	root.AddCommand(sync)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
```

Then run:

```sh
go mod init demo && go mod edit -replace github.com/spf13/cobra=/path/to/cobra && go mod tidy
go run . sync --remote --endpoint https://example.com   # succeeds: prints "syncing..."
go run . sync --remote                                  # conflict: rule "remote-requires-endpoint" is not satisfied by flags [endpoint remote]
go run . sync --remote --endpoint x --file f.txt        # conflict: rule "remote-excludes-file" ...
go run . __complete sync --remote --                    # completion: suggests --endpoint, but not --file
```

