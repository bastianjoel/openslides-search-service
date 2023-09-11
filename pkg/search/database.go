// SPDX-FileCopyrightText: 2022 Since 2011 Authors of OpenSlides, see https://github.com/OpenSlides/OpenSlides/blob/master/AUTHORS
//
// SPDX-License-Identifier: MIT

package search

import (
	"context"
	"fmt"
	log "github.com/sirupsen/logrus"
	"strconv"
	"strings"
	"time"

	"github.com/OpenSlides/openslides-search-service/pkg/config"
	"github.com/jackc/pgx/v5"
)

const (
	selectCollectionSizesSQL = `
SELECT
  count(*),
  left(fqid, position('/' IN fqid)-1) coll
FROM models
WHERE NOT deleted
GROUP BY coll`

	selectAllSQL = `
SELECT
  fqid,
  data::text,
  updated
FROM models
WHERE NOT deleted`

	selectDiffSQL = `
SELECT
  fqid,
  CASE WHEN updated > $1 THEN data::text ELSE NULL END,
  updated
FROM models
WHERE NOT deleted`
)

type entry struct {
	updated time.Time
	gen     uint16
}

// Database manages the updates needed to drive the text index.
type Database struct {
	cfg         *config.Config
	last        time.Time
	gen         uint16
	collections map[string]map[int]*entry
}

// NewDatabase creates a new database,
func NewDatabase(cfg *config.Config) *Database {
	return &Database{
		cfg: cfg,
	}
}

func (db *Database) run(fn func(context.Context, *pgx.Conn) error) error {
	ctx := context.Background()
	config, err := pgx.ParseConfig(db.cfg.Database.ConnectionConfig())
	if err != nil {
		return err
	}

	// Simple protocol is used for PGBouncer compatibility
	config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	con, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return err
	}
	defer con.Close(ctx)
	return fn(ctx, con)
}

func (db *Database) numEntries() int {
	if db.collections == nil {
		return 0
	}
	var sum int
	for _, col := range db.collections {
		sum += len(col)
	}
	return sum
}

func splitFqid(fqid string) (string, int, error) {
	col, idS, ok := strings.Cut(fqid, "/")
	if !ok {
		return "", 0, fmt.Errorf("invalid fqid: %q", fqid)
	}
	id, err := strconv.Atoi(idS)
	if err != nil {
		return "", 0, fmt.Errorf("invalid fqid: %q: %v", fqid, err)
	}
	return col, id, nil
}

type updateEventType int

const (
	addedEvent updateEventType = iota
	changedEvent
	removeEvent
)

type eventHandler func(evtType updateEventType, collection string, id int, data []byte) error

func nullEventHandler(updateEventType, string, int, []byte) error { return nil }

func (db *Database) update(handler eventHandler) error {
	start := time.Now()

	// Do not update if it is young enough.
	if !db.last.IsZero() && !start.After(db.last.Add(db.cfg.Index.Age)) {
		return nil
	}

	if handler == nil {
		handler = nullEventHandler
	}

	defer func() {
		log.Debugf("updating database took %v\n", time.Since(start))
	}()
	return db.run(func(ctx context.Context, conn *pgx.Conn) error {
		rows, err := conn.Query(ctx, selectDiffSQL, db.last)
		if err != nil {
			return err
		}
		defer rows.Close()

		before := db.numEntries()
		var unchanged, added, entries int

		ngen := db.gen + 1 // may overflow but thats okay.

		for rows.Next() {
			var (
				fqid    string
				data    []byte
				updated time.Time
			)
			if err := rows.Scan(&fqid, &data, &updated); err != nil {
				return err
			}
			entries++
			col, id, err := splitFqid(fqid)
			if err != nil {
				log.Errorf("error: %v\n", err)
				continue
			}
			// handle changed and new
			collection := db.collections[col]
			if collection == nil {
				collection = make(map[int]*entry)
				db.collections[col] = collection
			}
			e := collection[id]
			if e == nil {
				if err := handler(addedEvent, col, id, data); err != nil {
					return err
				}
				collection[id] = &entry{
					updated: updated,
					gen:     ngen,
				}
				added++
			} else {
				e.updated = updated
				e.gen = ngen
				if data != nil {
					if err := handler(changedEvent, col, id, data); err != nil {
						return err
					}
				} else {
					unchanged++
				}
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}

		// TODO: Do some clever arithmetics based on
		// before, entries, added and unchanged to
		// early stop this.
		var removed int
		if unchanged != before {
			for k, col := range db.collections {
				for id, e := range col {
					if e.gen != ngen {
						removed++
						delete(col, id)
						if err := handler(removeEvent, k, id, nil); err != nil {
							return err
						}
					}
				}
			}
		}

		log.Debugf("entries: %d / before: %d\n",
			entries, before)
		log.Debugf("unchanged: %d / added: %d / removed: %d\n",
			unchanged, added, removed)

		db.last = start
		db.gen = ngen
		return nil
	})
}

func preAllocCollections(ctx context.Context, conn *pgx.Conn) (map[string]map[int]*entry, error) {
	cols := make(map[string]map[int]*entry)
	rows, err := conn.Query(ctx, selectCollectionSizesSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var size int
		var col string
		if err := rows.Scan(&size, &col); err != nil {
			return nil, err
		}
		cols[col] = make(map[int]*entry, size)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return cols, nil
}

func (db *Database) fill(handler eventHandler) error {
	start := time.Now()
	defer func() {
		log.Infof("initial database fill took %v\n", time.Since(start))
	}()

	if handler == nil {
		handler = nullEventHandler
	}

	return db.run(func(ctx context.Context, conn *pgx.Conn) error {
		cols, err := preAllocCollections(ctx, conn)
		if err != nil {
			return err
		}
		rows, err := conn.Query(ctx, selectAllSQL)
		if err != nil {
			return err
		}
		defer rows.Close()
		var numEntries, size int

		for rows.Next() {
			var (
				fqid    string
				data    []byte
				updated time.Time
			)
			if err := rows.Scan(&fqid, &data, &updated); err != nil {
				return err
			}
			col, id, err := splitFqid(fqid)
			if err != nil {
				log.Warnf("error: %v\n", err)
				continue
			}
			collection := cols[col]
			if collection == nil {
				log.Warnf("alloc collection %q. This should has happend before.\n", col)
				collection = make(map[int]*entry)
				cols[col] = collection
			}
			if err := handler(addedEvent, col, id, data); err != nil {
				return err
			}

			size += len(data)

			collection[id] = &entry{
				updated: updated,
			}

			numEntries++
		}
		if err := rows.Err(); err != nil {
			return err
		}
		log.Debugf("num entries: %d / size: %d (%.2fMiB)\n",
			numEntries,
			size, float64(size)/(1024*1024))

		log.Debugf("num collections: %d\n", len(cols))
		db.collections = cols
		db.last = start
		return nil
	})
}
