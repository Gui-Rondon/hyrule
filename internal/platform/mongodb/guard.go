package mongodb

import (
	"context"
	"log/slog"
	"sync/atomic"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
)

// writeCommands are the commands that change data or collections.
var writeCommands = map[string]bool{
	"insert": true, "update": true, "delete": true, "findAndModify": true, "bulkWrite": true,
	"create": true, "createIndexes": true, "drop": true, "dropIndexes": true, "dropDatabase": true,
	"collMod": true, "renameCollection": true,
}

// WriteGuard watches the commands of a client in dry run: a write outside a
// transaction would be stored, because only transactions are rolled back.
// The driver cannot stop a command from a monitor, so the guard is a
// tripwire: it logs an error and counts the writes it saw; the code paths
// that write are the ones to fix.
type WriteGuard struct {
	violations atomic.Int64
}

// Monitor returns the command monitor to set in the client options.
func (g *WriteGuard) Monitor() *event.CommandMonitor {
	return &event.CommandMonitor{Started: g.started}
}

// Violations is the number of writes seen outside a transaction.
func (g *WriteGuard) Violations() int64 { return g.violations.Load() }

func (g *WriteGuard) started(ctx context.Context, e *event.CommandStartedEvent) {
	if !isWrite(e.CommandName, e.Command) {
		return
	}
	// every command of a transaction carries autocommit: false
	if _, err := e.Command.LookupErr("autocommit"); err == nil {
		return
	}
	g.violations.Add(1)
	collection, _ := e.Command.Lookup(e.CommandName).StringValueOK()
	slog.ErrorContext(ctx, "dry run: write outside a transaction, it will be stored",
		"command", e.CommandName, "database", e.DatabaseName, "collection", collection)
}

// isWrite tells whether a command writes, including an aggregation with
// $out or $merge.
func isWrite(name string, cmd bson.Raw) bool {
	if writeCommands[name] {
		return true
	}
	if name != "aggregate" {
		return false
	}
	stages, ok := cmd.Lookup("pipeline").ArrayOK()
	if !ok {
		return false
	}
	values, _ := stages.Values()
	for _, v := range values {
		stage, ok := v.DocumentOK()
		if !ok {
			continue
		}
		for _, op := range []string{"$out", "$merge"} {
			if _, err := stage.LookupErr(op); err == nil {
				return true
			}
		}
	}
	return false
}
