package db

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/sandboxws/gluon/internal/dsn"
)

// dbImages maps a container image repository to the driver family it serves.
//
// The image is the evidence, not the service name: a service called "db"
// running redis:7 is not a SQL database, and one called "warehouse" running
// postgres:16 is. Matching on the name would get both backwards.
var dbImages = map[string]string{
	"postgres": dsn.Postgres, "postgis/postgis": dsn.Postgres,
	"timescale/timescaledb": dsn.Postgres, "pgvector/pgvector": dsn.Postgres,
	"bitnami/postgresql": dsn.Postgres, "supabase/postgres": dsn.Postgres,
	"mysql": dsn.MySQL, "mariadb": dsn.MySQL, "percona": dsn.MySQL,
	"bitnami/mysql":                  dsn.MySQL,
	"mcr.microsoft.com/mssql/server": dsn.SQLServer,
}

// containerPort is where each family listens inside the network.
var containerPort = map[string]int{
	dsn.Postgres: 5432, dsn.MySQL: 3306, dsn.SQLServer: 1433,
}

// scanCompose reads database services out of a compose file.
//
// Two shape rules do the work. The document must decode to a mapping with a
// `services:` mapping under it — which is what rejects a Kubernetes manifest or
// a CI config that happens to carry the same filename. And a service must both
// run a known database image and publish a host port, because a database gluon
// cannot reach from this machine is not a database gluon can query.
func scanCompose(path string, depth int) []Candidate {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	root := docRoot(&doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return nil
	}
	services := mapValue(root, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil
	}

	var out []Candidate
	for i := 0; i+1 < len(services.Content); i += 2 {
		name, svc := services.Content[i].Value, services.Content[i+1]
		if svc.Kind != yaml.MappingNode {
			continue
		}
		image := scalar(mapValue(svc, "image"))
		family, ok := dbImages[imageRepo(image)]
		if !ok {
			continue
		}
		hostPort, published := publishedPort(svc, containerPort[family])
		env := serviceEnv(svc)

		c := Candidate{
			Rank:  RankInfra,
			Depth: depth,
			From: []Provenance{{
				File: path,
				Line: services.Content[i].Line,
				Key:  "services." + name + "  " + image,
				Kind: KindCompose,
			}},
		}
		if !published {
			// Kept, so :db can say why it is not usable, but it can never be
			// chosen: a port that exists only inside the compose network is not
			// one a REPL on this machine can open.
			c.Concerns = append(c.Concerns, "publishes no host port")
			hostPort = containerPort[family]
		}

		// The host is always 127.0.0.1 with the published port, never the
		// service name. A service hostname resolves inside the compose network
		// and nowhere else, so using it fails with a DNS error that blames the
		// wrong thing entirely.
		c.DSN = dsn.FromFields(family, "127.0.0.1", hostPort,
			envAny(env, "POSTGRES_USER", "MYSQL_USER", "MARIADB_USER", "MSSQL_USER"),
			envAny(env, "POSTGRES_DB", "MYSQL_DATABASE", "MARIADB_DATABASE"),
			"", nil)
		if pwKey := envKey(env, "POSTGRES_PASSWORD", "MYSQL_PASSWORD", "MYSQL_ROOT_PASSWORD", "MARIADB_PASSWORD"); pwKey != "" {
			c.Secret = SecretRef{Env: pwKey, File: path, Key: pwKey}
		}
		if !published {
			c.Concerns = append(c.Concerns, fmt.Sprintf("assumed port %d", hostPort))
		}
		out = append(out, c)
	}
	return out
}

func docRoot(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	return n
}

// mapValue looks up a key, following a merge alias.
//
// Merge keys are resolved by Decode, not present in the raw node tree, so a
// walker that did not follow `<<: *anchor` would silently see no environment at
// all in a compose file that uses x- anchors — a wrong answer with no symptom.
func mapValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Value == key {
			return resolveAlias(v)
		}
		if k.Tag == "!!merge" {
			if got := mapValue(resolveAlias(v), key); got != nil {
				return got
			}
		}
	}
	return nil
}

func resolveAlias(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

func scalar(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	return n.Value
}

// imageRepo strips a registry host and a tag or digest.
func imageRepo(image string) string {
	if image == "" {
		return ""
	}
	if i := strings.Index(image, "@"); i >= 0 {
		image = image[:i]
	}
	// A colon is a tag unless it is part of a registry host:port, which only
	// happens before the first slash.
	if i := strings.LastIndex(image, ":"); i >= 0 && !strings.Contains(image[i:], "/") {
		image = image[:i]
	}
	return image
}

// publishedPort finds the host port mapped to the database's container port.
func publishedPort(svc *yaml.Node, want int) (int, bool) {
	ports := mapValue(svc, "ports")
	if ports == nil || ports.Kind != yaml.SequenceNode {
		return 0, false
	}
	for _, entry := range ports.Content {
		e := resolveAlias(entry)
		switch e.Kind {
		case yaml.ScalarNode:
			if h, c, ok := parsePortMapping(e.Value); ok && (c == want || c == 0) {
				return h, true
			}
		case yaml.MappingNode:
			target, _ := strconv.Atoi(scalar(mapValue(e, "target")))
			pub, err := strconv.Atoi(scalar(mapValue(e, "published")))
			if err == nil && (target == want || target == 0) {
				return pub, true
			}
		}
	}
	return 0, false
}

// parsePortMapping reads the short forms: "5432", "5433:5432",
// "127.0.0.1:5433:5432", and any of them with a /tcp suffix.
func parsePortMapping(s string) (host, container int, ok bool) {
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/tcp"), "/udp")
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		n, err := strconv.Atoi(parts[0])
		return n, n, err == nil
	case 2:
		h, err1 := strconv.Atoi(parts[0])
		c, err2 := strconv.Atoi(parts[1])
		return h, c, err1 == nil && err2 == nil
	case 3:
		h, err1 := strconv.Atoi(parts[1])
		c, err2 := strconv.Atoi(parts[2])
		return h, c, err1 == nil && err2 == nil
	}
	return 0, 0, false
}

// serviceEnv reads `environment:` in both the mapping and the KEY=VALUE list
// form, because compose accepts either and projects use both.
func serviceEnv(svc *yaml.Node) map[string]string {
	out := map[string]string{}
	env := mapValue(svc, "environment")
	if env == nil {
		return out
	}
	switch env.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(env.Content); i += 2 {
			out[env.Content[i].Value] = resolveAlias(env.Content[i+1]).Value
		}
	case yaml.SequenceNode:
		for _, e := range env.Content {
			if k, v, ok := strings.Cut(resolveAlias(e).Value, "="); ok {
				out[k] = v
			}
		}
	}
	return out
}

func envAny(env map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := env[k]; ok && v != "" {
			return v
		}
	}
	return ""
}

func envKey(env map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := env[k]; ok && v != "" {
			return k
		}
	}
	return ""
}
