// Command instance-cli runs the operator actions of the application. None of
// them places an order.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/operations"
	"os"
	"time"
)

func run() error {
	action := flag.String("action", "", "init-storage, grant-storage or compile-calendar")
	source := flag.String("config", "config/config.toml", "Local config path (init-storage, grant-storage)")
	login := flag.String("login", "", "Login role to bind (grant-storage)")
	kind := flag.String("kind", "", "DYNAMIC or DYNAMIC_READ (grant-storage)")
	root := flag.String("root", ".", "Repository root (compile-calendar)")
	input := flag.String("input", "", "Calendar request file (compile-calendar)")
	output := flag.String("output", "", "New file under runtime/ (compile-calendar)")
	maxBytes := flag.Int("max-bytes", 0, "Byte limit for input and output (compile-calendar)")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	switch *action {
	case "init-storage", "grant-storage":
		admin, err := config.LoadStorageAdmin(*source)
		if err != nil {
			return err
		}
		if *action == "init-storage" {
			err = pg.InitializeStorage(ctx, admin.AdminDatabaseURL, admin.Binding())
		} else {
			err = pg.GrantStorage(ctx, admin.AdminDatabaseURL, admin.Binding(), *login, *kind)
		}
		if err != nil {
			return err
		}
		fmt.Println("INSTANCE_OPERATOR_OPERATION_RECORDED")
		return nil
	case "compile-calendar":
		if err := operations.CompileVenueCalendarFile(*root, *input, *output, *maxBytes, time.Now().UTC()); err != nil {
			return err
		}
		fmt.Println("INSTANCE_OPERATOR_OPERATION_RECORDED")
		return nil
	}
	return d.Fail("INSTANCE_CLI_ACTION_REQUIRED", 422)
}
func main() {
	if err := run(); err != nil {
		code := "INSTANCE_CLI_UNAVAILABLE"
		var known *d.Error
		if errors.As(err, &known) {
			code = known.Code
		}
		fmt.Fprintln(os.Stderr, code)
		os.Exit(1)
	}
}
