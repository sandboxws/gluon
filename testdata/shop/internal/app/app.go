// Package app wires the shop together in a samber/do container.
package app

import (
	"github.com/go-chi/chi/v5"
	"github.com/samber/do/v2"
	"github.com/spf13/viper"

	"example.com/shop/internal/api"
	"example.com/shop/internal/config"
	"example.com/shop/internal/rpc"
	"example.com/shop/internal/store"
)

// Injector is the container every service is built from. Nothing is built
// until something asks for it.
func Injector() do.Injector {
	i := do.New()
	do.Provide(i, func(do.Injector) (*viper.Viper, error) { return config.Load() })
	do.Provide(i, func(i do.Injector) (*store.Store, error) {
		v := do.MustInvoke[*viper.Viper](i)
		return store.Open(config.Resolve(v, "database.path"))
	})
	do.Provide(i, func(i do.Injector) (chi.Router, error) {
		return api.Routes(do.MustInvoke[*store.Store](i)), nil
	})
	do.Provide(i, func(do.Injector) (*rpc.Orders, error) { return rpc.NewOrders(), nil })
	return i
}
