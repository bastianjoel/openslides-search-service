// SPDX-FileCopyrightText: 2022 Since 2011 Authors of OpenSlides, see https://github.com/OpenSlides/OpenSlides/blob/master/AUTHORS
//
// SPDX-License-Identifier: MIT

// Package meta implements handling of the meta data model.
package meta

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"

	"gopkg.in/yaml.v3"
)

var (
	modelNum  atomic.Int32
	fieldNum  atomic.Int32
	filterNum atomic.Int32
)

func load[T any](r io.Reader) (T, error) {
	dec := yaml.NewDecoder(r)
	var t T
	if err := dec.Decode(&t); err != nil {
		var n T
		return n, err
	}
	return t, nil
}

func fetchRemote[T any](path string) (T, error) {
	resp, err := http.Get(path)
	if err != nil {
		var n T
		return n, err
	}
	if resp.StatusCode != http.StatusOK {
		var n T
		return n, fmt.Errorf("%s failed: %s (%d)",
			path, http.StatusText(resp.StatusCode), resp.StatusCode)
	}
	defer resp.Body.Close()
	return load[T](resp.Body)
}

func fetchLocal[T any](path string) (T, error) {
	in, err := os.Open(path)
	if err != nil {
		var n T
		return n, err
	}
	defer in.Close()
	return load[T](in)
}

// Fetch loads a meta model.
func Fetch[T any](path string) (T, error) {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return fetchRemote[T](path)
	}
	return fetchLocal[T](path)
}

func copyStrings(s []string) []string {
	if s == nil {
		return nil
	}
	t := make([]string, len(s))
	copy(t, s)
	return t
}
