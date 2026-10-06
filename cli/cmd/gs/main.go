// Command gs è la CLI di GitStack per persone e agenti (stile gh), su
// client generato dall'OpenAPI del gateway. Vedi cli/README.md.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/fathorMB/GitStack/cli/internal/cmd/root"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// version è impostata in build: -ldflags "-X main.version=<tag>".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := root.Run(ctx, cmdutil.New(version, cmdutil.System()), os.Args[1:])
	stop()
	os.Exit(code)
}
