// Package plugins is the registry of everything gluon ships with.
//
// It exists as its own package so that internal/repl depends on a list rather
// than on each plugin, and so adding one is a single line here.
package plugins

import (
	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/plugins/config"
	"github.com/sandboxws/gluon/internal/plugins/db"
	"github.com/sandboxws/gluon/internal/plugins/di"
	"github.com/sandboxws/gluon/internal/plugins/encoding"
	"github.com/sandboxws/gluon/internal/plugins/ids"
	"github.com/sandboxws/gluon/internal/plugins/rpc"
	"github.com/sandboxws/gluon/internal/plugins/stdlib"
	"github.com/sandboxws/gluon/internal/plugins/web"
)

// Builtin is every compiled-in plugin, in the order ties are broken: the first
// registration for a type or an alias keeps it.
func Builtin() []plugin.Plugin {
	return []plugin.Plugin{
		// The standard library, always active.
		stdlib.HTTP{},
		stdlib.JSON{},
		stdlib.XML{},
		stdlib.CSV{},
		stdlib.Time{},
		stdlib.Slog{},
		db.SQL{},

		// Third-party, active only when the session can see the module.
		ids.UUID{},
		ids.Decimal{},
		db.Gorm{},
		db.Sqlx{},
		db.Pgx{},
		db.Ent{},
		// Both migration libraries want :migrations, and they track applied
		// versions in differently shaped tables — so the tie-break decides which
		// table is read, not just which name is printed. goose is first because
		// its library API is the normal way to use it: a build list containing
		// goose almost always means the application itself runs the migrations,
		// while golang-migrate is as often present for a CLI somebody installed.
		// :plugins names the loser either way.
		db.Goose{},
		db.Migrate{},
		// Seven routers all want :routes. Order is the tie-break, and having
		// two of them in one build list is unusual enough — a service midway
		// between frameworks — that reporting the conflict beats inventing
		// seven different names.
		//
		// The three that shipped first keep their positions, and every
		// framework added since is appended in alphabetical order. Appending
		// is the rule rather than an accident: a session whose build list has
		// chi has been answered by chi since v7, and registering a new
		// framework ahead of it would silently change that answer.
		web.Chi{},
		web.Gin{},
		web.Echo{},
		web.Fiber{},
		web.Hertz{},
		web.Iris{},
		web.Mux{},
		web.Cobra{},
		// Both container plugins want :services and :graph, and the order here
		// is the whole tie-break. do is first because the two libraries are not
		// symmetric in a go.mod: fx depends on dig, so any fx project carries
		// dig transitively whether or not it holds a dig container, while
		// nothing in common use pulls samber/do in behind your back. A build
		// list with both is therefore far more often a samber/do project that
		// acquired dig than the reverse. :plugins names the loser either way.
		di.Do{},
		di.Dig{},
		// Both config libraries want :config. viper is first because it is by
		// a wide margin the more common one, and the order here is the whole
		// tie-break — :plugins names the loser rather than dropping it.
		config.Viper{},
		config.Koanf{},
		// Both yaml plugins want :yaml. They are the same library under two
		// module paths, so the tie-break decides nothing about the output —
		// only which of the two a session with both is told it is using, and
		// that should be the maintained one.
		encoding.YAML{},
		encoding.YAMLv3{},
		encoding.TOML{},
		encoding.MsgPack{},
		encoding.Protobuf{},
		// grpc is last for no reason beyond order of arrival: it shares no
		// command name with anything above it, so the tie-break never reaches
		// it.
		rpc.GRPC{},
	}
}
