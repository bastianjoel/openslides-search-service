// SPDX-FileCopyrightText: 2022 Since 2011 Authors of OpenSlides, see https://github.com/OpenSlides/OpenSlides/blob/master/AUTHORS
//
// SPDX-License-Identifier: MIT

// Package main implements the daemon of the search service.
package main

import (
	"context"
	"flag"
	"fmt"
	log "github.com/sirupsen/logrus"
	"os"
	"os/signal"
	"runtime"

	"github.com/OpenSlides/openslides-autoupdate-service/pkg/auth"
	"github.com/OpenSlides/openslides-autoupdate-service/pkg/environment"
	"github.com/OpenSlides/openslides-autoupdate-service/pkg/redis"
	"github.com/OpenSlides/openslides-search-service/pkg/config"
	"github.com/OpenSlides/openslides-search-service/pkg/meta"
	"github.com/OpenSlides/openslides-search-service/pkg/oserror"
	"github.com/OpenSlides/openslides-search-service/pkg/search"
	"github.com/OpenSlides/openslides-search-service/pkg/web"
	"golang.org/x/sys/unix"
)

func check(err error) {
	if err != nil {
		log.Fatalf("error: %v\n", err)
	}
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, unix.SIGTERM)
		<-sig
		cancel()
		<-sig
		os.Exit(2)
	}()
	return ctx, cancel
}

func run(cfg *config.Config) error {
	ctx, cancel := signalContext()
	defer cancel()

	models, err := meta.Fetch[meta.Collections](cfg.Models.Models)
	if err != nil {
		return fmt.Errorf("loading models failed: %w", err)
	}

	// For text indexing we can only use string fields.
	searchModels := models.Clone()

	// If there are search filters configured cut search models further down.
	if cfg.Models.Search != "" {
		searchFilter, err := meta.Fetch[meta.Filters](cfg.Models.Search)
		if err != nil {
			return fmt.Errorf("loading search filters failed. %w", err)
		}
		searchModels.Retain(searchFilter.Retain(false))
	} else {
		searchModels.Retain(meta.RetainStrings())
	}

	db := search.NewDatabase(cfg)
	ti, err := search.NewTextIndex(cfg, db, searchModels)
	if err != nil {
		return fmt.Errorf("creating text index failed: %w", err)
	}
	defer ti.Close()

	runtime.GC()

	qs, err := search.NewQueryServer(cfg, ti)
	if err != nil {
		return err
	}
	go qs.Run(ctx)

	lookup := new(environment.ForProduction)
	// Redis as message bus for datastore and logout events.
	messageBus := redis.New(lookup)
	// Auth Service.
	authService, authBackground := auth.New(lookup, messageBus)

	go authBackground(ctx, oserror.Handle)

	return web.Run(ctx, cfg, authService, qs, searchModels.CollectionRequestFields())
}

func main() {
	flag.Parse()
	cfg, err := config.GetConfig()
	check(err)
	check(run(cfg))
}
