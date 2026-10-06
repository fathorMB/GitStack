// Command gitstack è lo strumento di amministrazione dell'host GitStack
// (installazione, stato, e in seguito backup, restore, upgrade). Non è `gs`,
// la CLI degli utenti (cli/): vedi admin/README.md.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/fathorMB/GitStack/admin/internal/cli"
)

// version è impostata in build: -ldflags "-X main.version=<tag>".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.NewApp(version).Run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
