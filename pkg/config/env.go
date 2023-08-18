// SPDX-FileCopyrightText: 2022 Since 2011 Authors of OpenSlides, see https://github.com/OpenSlides/OpenSlides/blob/master/AUTHORS
//
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// parseSecretsFile takes a relative path as its argument
// and returns its contents
func parseSecretsFile(defaultFile string) func(string) (string, error) {
	return func(s string) (string, error) {
		if s == "" {
			s = defaultFile
		}

		content, err := os.ReadFile(s)
		if err != nil {
			return "", err
		}
		return string(content), nil
	}
}

// noparse returns an unparsed string.
func noparse(s string) (string, error) {
	return s, nil
}

// parseDuration returns a time.Duration. If the
// given string is an integer it is interpreted as seconds.
func parseDuration(s string) (time.Duration, error) {
	t, err := strconv.Atoi(s)
	if err == nil {
		return time.Duration(t) * time.Second, nil
	}
	return time.ParseDuration(s)
}

// store returns a function to parse a string to return a function to store a value.
func store[T any](parse func(string) (T, error)) func(*T) func(string) error {
	return func(dst *T) func(string) error {
		return func(s string) error {
			x, err := parse(s)
			if err != nil {
				return err
			}
			*dst = x
			return nil
		}
	}
}

// storeEnv maps the name of an env var to a store function.
type storeEnv struct {
	name  string
	store func(string) error
}

// store iterates over the given env/stores calls the store function
// of every env var that is found.
func storeFromEnv(se []storeEnv) error {
	for _, s := range se {
		if v, ok := os.LookupEnv(s.name); ok {
			if err := s.store(v); err != nil {
				return fmt.Errorf("parsing env var %q failed: %w", s.name, err)
			}
		}
	}
	return nil
}
