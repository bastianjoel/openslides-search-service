// SPDX-FileCopyrightText: 2022 Since 2011 Authors of OpenSlides, see https://github.com/OpenSlides/OpenSlides/blob/master/AUTHORS
//
// SPDX-License-Identifier: MIT

// Package web exposes a web api.
package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"

	"github.com/OpenSlides/openslides-autoupdate-service/pkg/auth"
	"github.com/OpenSlides/openslides-search-service/pkg/config"
	"github.com/OpenSlides/openslides-search-service/pkg/oserror"
	"github.com/OpenSlides/openslides-search-service/pkg/search"
)

type controller struct {
	cfg       *config.Config
	auth      *auth.Auth
	qs        *search.QueryServer
	reqFields map[string][]string
}

/*
func userIDFromRequest(r *http.Request) (int, error) {
	user := r.FormValue("u")
	if user == "" {
		return 0, errors.New("'u' parameter missing")
	}
	userID, err := strconv.Atoi(user)
	if err != nil {
		return 0, errors.New("'u' is not an user id")
	}
	return userID, nil
}
*/

func (c *controller) search(w http.ResponseWriter, r *http.Request) {

	query := r.FormValue("q")
	if query == "" {
		handleErrorWithStatus(w,
			invalidRequestError{
				errors.New("'q' parameter missing")})
		return
	}

	meeting, _ := strconv.Atoi(r.FormValue("m"))
	answers, err := c.qs.Query(query, meeting)
	if err != nil {
		handleErrorWithStatus(w, err)
		return
	}

	if c.cfg.Restricter.URL != "" {

		userID := c.auth.FromContext(r.Context())
		/*
			userID, err := userIDFromRequest(r)
			if err != nil {
				handleErrorWithStatus(w, err)
				return
			}
		*/

		requestedFields := map[string][]string{}
		for _, fqid := range answers {
			collection, _, _ := strings.Cut(fqid, "/")
			if _, ok := requestedFields[collection]; !ok {
				if _, ok := c.reqFields[collection]; ok {
					requestedFields[collection] = c.reqFields[collection]
				}
			}
		}

		requestBody := struct {
			UserID int                 `json:"user_id"`
			FQIDs  []string            `json:"fqids"`
			Fields map[string][]string `json:"fields"`
		}{
			UserID: userID,
			FQIDs:  answers,
			Fields: requestedFields,
		}

		body, err := json.Marshal(&requestBody)
		if err != nil {
			handleErrorWithStatus(w, err)
			return
		}
		resp, err := http.Post(
			c.cfg.Restricter.URL,
			"application/json",
			bytes.NewReader(body))
		if err != nil {
			handleErrorWithStatus(w, err)
			return
		}
		if resp.StatusCode != http.StatusOK {
			handleErrorWithStatus(w,
				invalidRequestError{
					fmt.Errorf("restricter call failed: %q (%d)",
						resp.Status, resp.StatusCode)})
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")

		filteredResp, err := filterRestricterResponse(resp.Body)
		if err != nil {
			handleErrorWithStatus(w, err)
			return
		}

		if _, err := w.Write(filteredResp); err != nil {
			log.Printf("error: writing response failed: %v\n", err)
		}
		return
	}

	// No restricter configured.

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(answers); err != nil {
		log.Printf("error: %v\n", err)
	}
}

// removes restriced results from restricter response by checking
// for id fields within the retured results
func filterRestricterResponse(body io.ReadCloser) ([]byte, error) {
	respBody, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}

	var restricterResponse map[string]map[string]any
	if err := json.Unmarshal(respBody, &restricterResponse); err != nil {
		return nil, err
	}

	for k, v := range restricterResponse {
		if _, ok := v["id"]; !ok {
			delete(restricterResponse, k)
		}
	}

	filteredContent, err := json.Marshal(restricterResponse)
	if err != nil {
		return nil, err
	}

	return filteredContent, nil
}

func authMiddleware(next http.Handler, auth *auth.Auth) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, err := auth.Authenticate(w, r)
		if err != nil {
			handleErrorWithStatus(w, fmt.Errorf("authenticate request: %w", err))
			return
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type invalidRequestError struct {
	err error
}

func (e invalidRequestError) Error() string {
	return fmt.Sprintf("Invalid request: %v", e.err)
}

func (e invalidRequestError) Type() string {
	return "invalid_request"
}

func handleErrorWithStatus(w http.ResponseWriter, err error) {
	handleError(w, err, true, false)
}

// ClientError is an expected error that are returned to the client.
type ClientError interface {
	Type() string
	Error() string
}

// handleError interprets the given error and writes a corresponding message to
// the client and/or stdout.
//
// Do not use this function directly but use handleErrorWithStatus,
// handleErrorWithoutStatus or handleErrorInternal.
//
// If the handler already started to write the body then it is not allowed to
// set the http-status-code. In this case, writeStatusCode has to be fales.
func handleError(w http.ResponseWriter, err error, writeStatusCode bool, internal bool) {
	if writeStatusCode {
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	if oserror.ContextDone(err) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) {
		// Client closed connection.
		return
	}

	status := http.StatusBadRequest
	var StatusCoder interface{ StatusCode() int }
	if errors.As(err, &StatusCoder) {
		status = StatusCoder.StatusCode()
	}

	var errClient ClientError
	if errors.As(err, &errClient) {
		if writeStatusCode {
			w.WriteHeader(status)
		}

		fmt.Fprintf(w, `{"error": {"type": "%s", "msg": "%s"}}`,
			errClient.Type(), quote(errClient.Error()))
		return
	}

	if writeStatusCode {
		w.WriteHeader(http.StatusInternalServerError)
	}

	clientOutput := `{"error": {"type": "InternalError", "msg": "Something went wrong on the server. The admin is already informed."}}`
	if internal {
		clientOutput = err.Error()
	}

	oserror.Handle(err)
	fmt.Fprintln(w, clientOutput)
}

// quote decodes changes quotation marks with a backslash to make sure, they are
// valid json.
func quote(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}

// Run starts the web server and routes the incoming requests to the controller.
func Run(
	ctx context.Context,
	cfg *config.Config,
	auth *auth.Auth,
	qs *search.QueryServer,
	reqFields map[string][]string,
) error {

	c := controller{
		cfg:       cfg,
		auth:      auth,
		qs:        qs,
		reqFields: reqFields,
	}

	mux := http.NewServeMux()

	mux.Handle(
		"/system/search",
		authMiddleware(http.HandlerFunc(c.search), auth))

	addr := fmt.Sprintf("%s:%d", cfg.Web.Host, cfg.Web.Port)
	log.Printf("listen web on %s\n", addr)

	s := &http.Server{
		Addr:        addr,
		Handler:     mux,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	done := make(chan error)
	go func() {
		<-ctx.Done()
		if err := s.Shutdown(context.Background()); err != nil {
			done <- fmt.Errorf("server error: %v", err)
			return
		}
		log.Println("web server done")
		done <- nil
	}()
	if err := s.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("server failed: %v", err)
	}
	return <-done
}
