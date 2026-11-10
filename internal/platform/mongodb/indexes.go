package mongodb

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/Gui-Rondon/hyrule/internal/platform/config"
)

// IndexSpec is one index of a <service>.indices.conf file or of the
// mongo.indexes config list. One per line:
//
//	# collection  keys                               options
//	settings      product:1                          unique
//	articles      classification_id:1,_updated:-1    name=by_classification
//	articles      friendly_title:1                   unique sparse
//	sessions      created:1                          ttl=3600
//
// Directions: 1, -1, text, hashed, 2dsphere. The name defaults to Mongo's
// (field_dir joined by "_").
type IndexSpec struct {
	Collection string
	Keys       bson.D
	Name       string
	Unique     bool
	Sparse     bool
	TTL        *int32
}

// IndexesMode is what EnsureIndexes does with the missing indexes.
type IndexesMode string

const (
	// IndexesCreate builds the missing indexes at startup (the default).
	IndexesCreate IndexesMode = "create"
	// IndexesCheck only logs the missing ones, for collections where a
	// build at startup is too heavy and the index is created by hand.
	IndexesCheck IndexesMode = "check"
	// IndexesOff ignores the declared indexes.
	IndexesOff IndexesMode = "off"
)

// IndexesModeFromConfig reads mongo.indexes_mode.
func IndexesModeFromConfig(cfg config.Config) (IndexesMode, error) {
	switch m := IndexesMode(cfg.String("mongo.indexes_mode", "")); m {
	case "":
		return IndexesCreate, nil
	case IndexesCreate, IndexesCheck, IndexesOff:
		return m, nil
	default:
		return "", fmt.Errorf("mongo.indexes_mode: unknown mode %q (create, check, off)", m)
	}
}

// ParseIndexes reads the indices.conf format.
func ParseIndexes(r io.Reader) ([]IndexSpec, error) {
	var specs []IndexSpec
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		spec, err := parseIndexLine(line)
		if err != nil {
			return nil, fmt.Errorf("indexes: line %d: %w", n, err)
		}
		specs = append(specs, spec)
	}
	return specs, sc.Err()
}

var indexDirections = map[string]any{"1": 1, "-1": -1, "text": "text", "hashed": "hashed", "2dsphere": "2dsphere"}

func parseIndexLine(line string) (IndexSpec, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return IndexSpec{}, fmt.Errorf("%q: expected <collection> <field:dir,...> [options]", line)
	}
	spec := IndexSpec{Collection: fields[0]}
	var names []string
	seen := map[string]bool{}
	for _, key := range strings.Split(fields[1], ",") {
		field, dir, ok := strings.Cut(key, ":")
		value, valid := indexDirections[dir]
		if !ok || field == "" || !valid {
			return IndexSpec{}, fmt.Errorf("%q: key must be field:1, field:-1, field:text, field:hashed or field:2dsphere", key)
		}
		if seen[field] {
			return IndexSpec{}, fmt.Errorf("%q: field %s repeated", fields[1], field)
		}
		seen[field] = true
		spec.Keys = append(spec.Keys, bson.E{Key: field, Value: value})
		names = append(names, field+"_"+dir)
	}
	spec.Name = strings.Join(names, "_")
	for _, opt := range fields[2:] {
		k, v, _ := strings.Cut(opt, "=")
		switch k {
		case "unique":
			spec.Unique = true
		case "sparse":
			spec.Sparse = true
		case "name":
			if v == "" {
				return IndexSpec{}, fmt.Errorf("%q: empty name", opt)
			}
			spec.Name = v
		case "ttl":
			secs, err := strconv.ParseInt(v, 10, 32)
			if err != nil || secs < 0 {
				return IndexSpec{}, fmt.Errorf("%q: ttl must be seconds", opt)
			}
			ttl := int32(secs)
			spec.TTL = &ttl
		default:
			return IndexSpec{}, fmt.Errorf("%q: unknown option (unique, sparse, name=, ttl=)", opt)
		}
	}
	return spec, nil
}

// MergeIndexes adds the lines of mongo.indexes (config-files) to the indexes
// of the service file; a config line with the name of a file index replaces
// it.
func MergeIndexes(file []IndexSpec, cfg config.Config) ([]IndexSpec, error) {
	extra, err := ParseIndexes(strings.NewReader(strings.Join(cfg.Strings("mongo.indexes"), "\n")))
	if err != nil {
		return nil, fmt.Errorf("mongo.indexes: %w", err)
	}
	out := append([]IndexSpec(nil), file...)
	for _, e := range extra {
		replaced := false
		for i := range out {
			if out[i].Collection == e.Collection && out[i].Name == e.Name {
				out[i], replaced = e, true
			}
		}
		if !replaced {
			out = append(out, e)
		}
	}
	return out, nil
}

// Server codes: an index that already exists with other options or name,
// and a collection that does not exist yet.
const (
	codeIndexOptionsConflict  = 85
	codeIndexKeySpecsConflict = 86
	codeNamespaceNotFound     = 26
)

func hasCode(err error, codes ...int) bool {
	var srvErr mongo.ServerError
	if !errors.As(err, &srvErr) {
		return false
	}
	for _, c := range codes {
		if srvErr.HasErrorCode(c) {
			return true
		}
	}
	return false
}

// EnsureIndexes compares the declared indexes with the ones in db and
// returns the missing ones. With IndexesCreate they are built; an index
// that exists under another name or with other options is logged and kept.
func EnsureIndexes(ctx context.Context, db *mongo.Database, specs []IndexSpec, mode IndexesMode) ([]IndexSpec, error) {
	if mode == IndexesOff {
		return nil, nil
	}
	existing := map[string]map[string]bool{}
	var missing []IndexSpec
	for _, s := range specs {
		names, ok := existing[s.Collection]
		if !ok {
			var err error
			if names, err = indexNamesOf(ctx, db.Collection(s.Collection)); err != nil {
				return nil, err
			}
			existing[s.Collection] = names
		}
		if !names[s.Name] {
			missing = append(missing, s)
		}
	}
	if mode == IndexesCheck {
		for _, s := range missing {
			slog.Warn("mongodb: index missing", "collection", s.Collection, "index", s.Name)
		}
		return missing, nil
	}

	for _, s := range missing {
		opts := options.Index().SetName(s.Name)
		if s.Unique {
			opts.SetUnique(true)
		}
		if s.Sparse {
			opts.SetSparse(true)
		}
		if s.TTL != nil {
			opts.SetExpireAfterSeconds(*s.TTL)
		}
		_, err := db.Collection(s.Collection).Indexes().CreateOne(ctx, mongo.IndexModel{Keys: s.Keys, Options: opts})
		if hasCode(err, codeIndexOptionsConflict, codeIndexKeySpecsConflict) {
			slog.Warn("mongodb: index exists with another name or options, kept as is",
				"collection", s.Collection, "index", s.Name, "error", err)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("mongodb: create index %s.%s: %w", s.Collection, s.Name, err)
		}
		slog.Info("mongodb: index created", "collection", s.Collection, "index", s.Name)
	}
	return nil, nil
}

func indexNamesOf(ctx context.Context, coll *mongo.Collection) (map[string]bool, error) {
	cur, err := coll.Indexes().List(ctx)
	if err != nil {
		if hasCode(err, codeNamespaceNotFound) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	var docs []struct {
		Name string `bson:"name"`
	}
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, d := range docs {
		names[d.Name] = true
	}
	return names, nil
}
