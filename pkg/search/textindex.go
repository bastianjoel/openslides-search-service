// SPDX-FileCopyrightText: 2022 Since 2011 Authors of OpenSlides, see https://github.com/OpenSlides/OpenSlides/blob/master/AUTHORS
//
// SPDX-License-Identifier: MIT

package search

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/OpenSlides/openslides-search-service/pkg/config"
	"github.com/OpenSlides/openslides-search-service/pkg/meta"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis"
	"github.com/blevesearch/bleve/v2/analysis/char/html"
	"github.com/blevesearch/bleve/v2/analysis/lang/de"
	"github.com/blevesearch/bleve/v2/analysis/token/lowercase"
	"github.com/blevesearch/bleve/v2/analysis/tokenizer/unicode"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/registry"
	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/buger/jsonparser"
)

// TextIndex manages a text index over a given database.
type TextIndex struct {
	cfg          *config.Config
	db           *Database
	collections  meta.Collections
	indexMapping mapping.IndexMapping
	index        bleve.Index
}

// NewTextIndex creates a new text index.
func NewTextIndex(
	cfg *config.Config,
	db *Database,
	collections meta.Collections,
) (*TextIndex, error) {
	ti := &TextIndex{
		cfg:          cfg,
		db:           db,
		collections:  collections,
		indexMapping: buildIndexMapping(collections),
	}

	if err := ti.build(); err != nil {
		return nil, err
	}

	return ti, nil
}

// Close tears down an open text index.
func (ti *TextIndex) Close() error {
	if ti == nil {
		return nil
	}
	var err1 error
	if index := ti.index; index != nil {
		ti.index = nil
		err1 = index.Close()
	}
	if err2 := os.RemoveAll(ti.cfg.Index.File); err1 == nil {
		err1 = err2
	}
	return err1
}

const deHTML = "de_html"

func deHTMLAnalyzerConstructor(
	config map[string]interface{},
	cache *registry.Cache,
) (analysis.Analyzer, error) {

	htmlFilter, err := cache.CharFilterNamed(html.Name)
	if err != nil {
		return nil, err
	}
	unicodeTokenizer, err := cache.TokenizerNamed(unicode.Name)
	if err != nil {
		return nil, err
	}
	toLowerFilter, err := cache.TokenFilterNamed(lowercase.Name)
	if err != nil {
		return nil, err
	}
	stopDeFilter, err := cache.TokenFilterNamed(de.StopName)
	if err != nil {
		return nil, err
	}
	normalizeDeFilter, err := cache.TokenFilterNamed(de.NormalizeName)
	if err != nil {
		return nil, err
	}
	lightStemmerDeFilter, err := cache.TokenFilterNamed(de.LightStemmerName)
	if err != nil {
		return nil, err
	}
	rv := analysis.DefaultAnalyzer{
		CharFilters: []analysis.CharFilter{htmlFilter},
		Tokenizer:   unicodeTokenizer,
		TokenFilters: []analysis.TokenFilter{
			toLowerFilter,
			stopDeFilter,
			normalizeDeFilter,
			lightStemmerDeFilter,
		},
	}
	return &rv, nil
}

func init() {
	registry.RegisterAnalyzer(deHTML, deHTMLAnalyzerConstructor)
}

type bleveType map[string]any

func newBleveType(typ string) bleveType {
	return bleveType{"_bleve_type": typ}
}

func (bt bleveType) BleveType() any {
	return bt["_bleve_type"]
}

func buildIndexMapping(collections meta.Collections) mapping.IndexMapping {

	numberFieldMapping := bleve.NewNumericFieldMapping()
	textFieldMapping := bleve.NewTextFieldMapping()
	textFieldMapping.Analyzer = de.AnalyzerName

	htmlFieldMapping := bleve.NewTextFieldMapping()
	htmlFieldMapping.Analyzer = deHTML

	indexMapping := mapping.NewIndexMapping()

	for name, col := range collections {
		docMapping := bleve.NewDocumentMapping()
		for fname, cf := range col.Fields {
			if cf.Searchable {
				switch cf.Type {
				case "HTMLStrict", "HTMLPermissive":
					docMapping.AddFieldMappingsAt(fname, htmlFieldMapping)
				case "string", "text":
					docMapping.AddFieldMappingsAt(fname, textFieldMapping)
				case "relation", "number":
					docMapping.AddFieldMappingsAt(fname, numberFieldMapping)
				default:
					log.Printf("unsupport type %q\n", cf.Type)
				}
			}
		}
		indexMapping.AddDocumentMapping(name, docMapping)
	}

	return indexMapping
}

func (bt bleveType) fill(fields map[string]*meta.Member, data []byte) {
	for fname := range fields {
		if v, err := jsonparser.GetString(data, fname); err == nil {
			bt[fname] = v
		} else if v, err := jsonparser.GetInt(data, fname); err == nil {
			bt[fname] = v
		} else {
			delete(bt, fname)
		}
	}
}

func (ti *TextIndex) update() error {

	batch, batchCount := ti.index.NewBatch(), 0

	if err := ti.db.update(func(
		evt updateEventType,
		col string, id int, data []byte,
	) error {
		// we dont care if its not an indexed type.
		mcol := ti.collections[col]
		if mcol == nil {
			return nil
		}
		fqid := col + "/" + strconv.Itoa(id)
		switch evt {
		case addedEvent:
			bt := newBleveType(col)
			bt.fill(mcol.Fields, data)
			batch.Index(fqid, bt)

		case changedEvent:
			batch.Delete(fqid)
			bt := newBleveType(col)
			bt.fill(mcol.Fields, data)
			batch.Index(fqid, bt)

		case removeEvent:
			batch.Delete(fqid)
		}
		if batchCount++; batchCount >= ti.cfg.Index.Batch {
			if err := ti.index.Batch(batch); err != nil {
				return err
			}
			batch, batchCount = ti.index.NewBatch(), 0
		}
		return nil
	}); err != nil {
		return err
	}

	if batchCount > 0 {
		if err := ti.index.Batch(batch); err != nil {
			return err
		}
	}

	return nil
}

func (ti *TextIndex) build() error {
	start := time.Now()
	defer func() {
		log.Printf("building initial text index took %v\n", time.Since(start))
	}()

	// Remove old index file
	if _, err := os.Stat(ti.cfg.Index.File); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf(
				"checking index file %q failed: %w", ti.cfg.Index.File, err)
		}
	} else {
		if err := os.RemoveAll(ti.cfg.Index.File); err != nil {
			return fmt.Errorf(
				"removing index file %q failed: %w", ti.cfg.Index.File, err)
		}
	}

	index, err := bleve.New(ti.cfg.Index.File, ti.indexMapping)
	if err != nil {
		return fmt.Errorf(
			"opening index file %q failed: %w", ti.cfg.Index.File, err)
	}

	batch, batchCount := index.NewBatch(), 0

	if err := ti.db.fill(func(_ updateEventType, col string, id int, data []byte) error {
		// Dont care for collections which are not text indexed.
		mcol := ti.collections[col]
		if mcol == nil {
			return nil
		}
		bt := newBleveType(col)
		bt.fill(mcol.Fields, data)

		fqid := col + "/" + strconv.Itoa(id)
		batch.Index(fqid, bt)
		if batchCount++; batchCount >= ti.cfg.Index.Batch {
			if err := index.Batch(batch); err != nil {
				return fmt.Errorf("writing batch failed: %w", err)
			}
			batch, batchCount = index.NewBatch(), 0
		}
		return nil
	}); err != nil {
		index.Close()
		return err
	}

	if batchCount > 0 {
		if err := index.Batch(batch); err != nil {
			index.Close()
			return fmt.Errorf("writing batch failed: %w", err)
		}
	}

	ti.index = index

	return nil
}

// Search queries the internal index for hits.
func (ti *TextIndex) Search(question string, meetingID int) ([]string, error) {
	start := time.Now()
	defer func() {
		log.Printf("searching for %q took %v\n", question, time.Since(start))
	}()

	var q query.Query
	matchQuery := bleve.NewMatchQuery(question)
	matchQuery.Fuzziness = 1

	if meetingID > 0 {
		meetingQuery := bleve.NewQueryStringQuery("+meeting_id:" + strconv.Itoa(meetingID))

		q = bleve.NewConjunctionQuery(matchQuery, meetingQuery)
	} else {
		q = matchQuery
	}

	request := bleve.NewSearchRequest(q)
	result, err := ti.index.Search(request)
	if err != nil {
		return nil, err
	}
	log.Printf("number hits: %d\n", len(result.Hits))
	dupes := map[string]struct{}{}
	answers := make([]string, 0, len(result.Hits))
	numDupes := 0

	for i := range result.Hits {
		fqid := result.Hits[i].ID
		if _, ok := dupes[fqid]; ok {
			numDupes++
			continue
		}
		dupes[fqid] = struct{}{}
		answers = append(answers, fqid)
	}
	log.Printf("number of duplicates: %d\n", numDupes)
	return answers, nil
}
