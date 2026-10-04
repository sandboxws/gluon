// Command shopctl runs and looks after the shop.
package main

import (
	"os"

	"example.com/shop/internal/cli"
)

func main() {
	if err := cli.Root().Execute(); err != nil {
		os.Exit(1)
	}
}
