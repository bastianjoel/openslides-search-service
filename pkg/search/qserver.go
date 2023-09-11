// SPDX-FileCopyrightText: 2022 Since 2011 Authors of OpenSlides, see https://github.com/OpenSlides/OpenSlides/blob/master/AUTHORS
//
// SPDX-License-Identifier: MIT

// Package search implements the searching in a given database.
package search

import (
	"context"
	"errors"
	log "github.com/sirupsen/logrus"
	"time"

	"github.com/OpenSlides/openslides-search-service/pkg/config"
)

type queryItem struct {
	q       string
	meeting int
	fn      func(map[string]Answer, error)
}

// QueryServer manages incoming queries against the database.
type QueryServer struct {
	queries chan queryItem
	ti      *TextIndex
	cfg     *config.Config
}

// NewQueryServer creates a new query server with the help of a text index.
func NewQueryServer(cfg *config.Config, ti *TextIndex) (*QueryServer, error) {
	return &QueryServer{
		queries: make(chan queryItem, cfg.Web.MaxQueue),
		ti:      ti,
		cfg:     cfg,
	}, nil
}

// Run starts the query server.
func (qs *QueryServer) Run(ctx context.Context) {
	ticker := time.NewTicker(qs.cfg.Index.Update)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("shutting down query server")
			return
		case <-ticker.C:
			if err := qs.ti.update(); err != nil {
				log.Errorf("updating text index failed: %v\n", err)
			}
		case qi := <-qs.queries:
			// update the database before searching
			if err := qs.ti.update(); err != nil {
				qi.fn(nil, err)
				continue
			}
			qi.fn(qs.ti.Search(qi.q, qi.meeting))
		}
	}
}

var errQueryQueueFull = errors.New("query queue full")

// Query searches the database for hits. Returns a list of fqids.
func (qs *QueryServer) Query(q string, meeting int) (answers map[string]Answer, err error) {
	done := make(chan struct{})
	select {
	case qs.queries <- queryItem{
		q:       q,
		meeting: meeting,
		fn: func(as map[string]Answer, e error) {
			answers, err = as, e
			close(done)
		},
	}:
	default:
		return nil, errQueryQueueFull
	}
	<-done
	return
}
