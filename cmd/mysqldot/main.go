package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/maheshrijal/mysqldot/internal/cli"
)

var version = "dev"

func main() {
	if err := cli.New(version).Execute(); err != nil {
		code := 1
		var exit cli.ExitError
		if errors.As(err, &exit) {
			code = exit.Code
		}
		fmt.Fprintln(os.Stderr, "mysqldot:", err)
		os.Exit(code)
	}
}
