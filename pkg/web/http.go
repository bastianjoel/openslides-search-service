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
type auFields struct {
	RelationType string              `json:"type"`
	Collection   string              `json:"collection"`
	Fields       map[string]auFields `json:"fields"`
}

type auRequest struct {
	Ids        []int                `json:"ids"`
	Collection string               `json:"collection"`
	Fields     map[string]*auFields `json:"fields"`
}

func (c *controller) autoupdateRequestFromFQIDs(answers map[string]search.Answer) []auRequest {
	collIdxMap := map[string]int{}
	var req []auRequest
	for fqid := range answers {
		collection, id, found := strings.Cut(fqid, "/")
		if !found {
			continue
		}

		if _, ok := collIdxMap[collection]; !ok {
			collIdxMap[collection] = len(req)
			req = append(req, auRequest{
				Ids:        []int{},
				Collection: collection,
				Fields:     map[string]*auFields{},
			})

			if fields, ok := c.reqFields[collection]; ok {
				for _, field := range fields {
					req[collIdxMap[collection]].Fields[field] = nil
				}
			}
		}

		if parsedID, err := strconv.Atoi(id); err == nil {
			req[collIdxMap[collection]].Ids = append(req[collIdxMap[collection]].Ids, parsedID)
		}
	}
	return req
}

func (c *controller) search(w http.ResponseWriter, r *http.Request) {

	query := r.FormValue("q")
	if query == "" {
		handleErrorWithStatus(w,
			invalidRequestError{
				errors.New("'q' parameter missing")})
		return
	}

	answers, err := c.qs.Query(query)
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

		requestBody := c.autoupdateRequestFromFQIDs(answers)
		if len(requestBody) == 0 {
			if _, err := w.Write([]byte("{}")); err != nil {
				log.Printf("error: writing response failed: %v\n", err)
			}
			return
		}

		body, err := json.Marshal(&requestBody)
		if err != nil {
			handleErrorWithStatus(w, err)
			return
		}

		urlParams := fmt.Sprintf("?user_id=%d&single=1", userID)
		req, err := http.NewRequest("POST", c.cfg.Restricter.URL+urlParams, bytes.NewReader(body))
		if err != nil {
			handleErrorWithStatus(w, err)
			return
		}

		req.Header = http.Header{
			"Content-Type": {"application/json"},
		}

		client := http.Client{}
		resp, err := client.Do(req)
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

		filteredResp, err := transformRestricterResponse(answers, resp.Body)
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

// transforms the autoupdate response to per fqid objects
func transformRestricterResponse(answers map[string]search.Answer, body io.ReadCloser) ([]byte, error) {
	respBody, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}

	var restricterResponse map[string]any
	if err := json.Unmarshal(respBody, &restricterResponse); err != nil {
		return nil, err
	}

	type resultEntry struct {
		Content      map[string]any      `json:"content"`
		MatchedWords map[string][]string `json:"matched_by,omitempty"`
		Score        *float64            `json:"score,omitempty"`
	}
	transformed := make(map[string]resultEntry)
	for k, v := range restricterResponse {
		parts := strings.Split(k, "/")
		if len(parts) >= 3 {
			fqid := parts[0] + "/" + parts[1]
			field := parts[2]

			if _, ok := transformed[fqid]; !ok {
				var score *float64
				var matchedWords map[string][]string
				if val, ok := answers[fqid]; ok {
					score = &val.Score
					matchedWords = val.MatchedWords
				}
				transformed[fqid] = resultEntry{
					Content:      make(map[string]any),
					MatchedWords: matchedWords,
					Score:        score,
				}
			}

			transformed[fqid].Content[field] = v
		}
	}

	transformedContent, err := json.Marshal(transformed)
	if err != nil {
		return nil, err
	}

	return transformedContent, nil
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
