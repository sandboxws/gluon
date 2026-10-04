// Package cli is shopctl, the shop's command line.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Root is shopctl and everything under it.
func Root() *cobra.Command {
	root := &cobra.Command{Use: "shopctl", Short: "run and look after the shop"}
	root.PersistentFlags().String("config", "config.yaml", "the settings file")

	serve := &cobra.Command{Use: "serve", Short: "serve HTTP and gRPC", RunE: todo}
	serve.Flags().String("http", "127.0.0.1:8765", "where HTTP listens")
	serve.Flags().String("grpc", "127.0.0.1:50051", "where gRPC listens")

	migrate := &cobra.Command{Use: "migrate", Short: "change the database's schema"}
	migrate.AddCommand(
		&cobra.Command{Use: "up", Short: "apply every pending migration", RunE: todo},
		&cobra.Command{Use: "down", Short: "roll the newest one back", RunE: todo},
		&cobra.Command{Use: "status", Short: "what is applied, and when", RunE: todo},
	)

	users := &cobra.Command{Use: "users", Short: "customers"}
	list := &cobra.Command{Use: "list", Short: "every customer", RunE: todo}
	list.Flags().String("plan", "", "only this plan")
	users.AddCommand(list, &cobra.Command{Use: "add <email>", Short: "add a customer", Args: cobra.ExactArgs(1), RunE: todo})

	debug := &cobra.Command{Use: "debug", Short: "dump the container", Hidden: true, RunE: todo}

	root.AddCommand(serve, migrate, users, debug)
	return root
}

func todo(cmd *cobra.Command, _ []string) error {
	return fmt.Errorf("%s: not in this fixture", cmd.CommandPath())
}
