// SPDX-FileCopyrightText: 2022 Since 2011 Authors of OpenSlides, see https://github.com/OpenSlides/OpenSlides/blob/master/AUTHORS
//
// SPDX-License-Identifier: MIT

// Package main implements the generation of the filter used
// for the searchable fields.
package main

import (
	"bufio"
	"flag"
	"fmt"
	log "github.com/sirupsen/logrus"
	"os"

	"github.com/OpenSlides/openslides-search-service/pkg/meta"
)

const (
	backend   = "https://raw.githubusercontent.com/OpenSlides/openslides-backend"
	branch    = "master"
	modelsYML = "global/meta/models.yml"
	path      = backend + "/" + branch + "/" + modelsYML
)

func nolog(format string, args ...any) {}

func run(input, filter, output string, verbose bool) error {

	lg := nolog
	if verbose {
		lg = log.Printf
		log.SetLevel(log.TraceLevel)
	}

	lg("Fetching models from %s\n", input)

	collections, err := meta.Fetch[meta.Collections](input)
	if err != nil {
		return fmt.Errorf("loading input failed: %w", err)
	}

	var f *os.File

	var out *bufio.Writer
	if output != "" {
		if f, err = os.Create(output); err != nil {
			return fmt.Errorf("creating output failed: %w", err)
		}
		out = bufio.NewWriter(f)
	} else {
		out = bufio.NewWriter(os.Stdout)
	}

	check := func(e error) {
		if err == nil {
			err = e
		}
	}

	lg("Writing filter\n")
	searchable := meta.RetainStrings()
	collections.Retain(func(k, fk string, f *meta.Member) bool {
		switch fk {
		case "id", "sequential_number":
			f.Searchable = false
			return true
		case "meeting_id":
			f.Searchable = true
			return true
		default:
			f.Searchable = true
			return searchable(k, fk, f)
		}
	})

	check(collections.AsFilters().Write(out))
	check(out.Flush())
	if f != nil {
		check(f.Close())
	}
	return err
}

func main() {
	var (
		input   = flag.String("input", path, "source of input")
		filter  = flag.String("filter", "", "source of filter")
		output  = flag.String("output", "", "output file (default STDOUT)")
		verbose = flag.Bool("verbose", false, "verbose logging")
	)
	flag.Parse()
	if err := run(*input, *filter, *output, *verbose); err != nil {
		log.Fatalf("error: %v\n", err)
	}
}
