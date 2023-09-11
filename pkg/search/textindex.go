// SPDX-FileCopyrightText: 2022 Since 2011 Authors of OpenSlides, see https://github.com/OpenSlides/OpenSlides/blob/master/AUTHORS
//
// SPDX-License-Identifier: MIT

package search

import (
	"fmt"
	"html"
	"os"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/OpenSlides/openslides-search-service/pkg/config"
	"github.com/OpenSlides/openslides-search-service/pkg/meta"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis"
	bleveHtml "github.com/blevesearch/bleve/v2/analysis/char/html"
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

const deStandard = "standard_de"

func deStandardAnalyzerConstructor(config map[string]interface{}, cache *registry.Cache) (analysis.Analyzer, error) {
	tokenizer, err := cache.TokenizerNamed(unicode.Name)
	if err != nil {
		return nil, err
	}
	toLowerFilter, err := cache.TokenFilterNamed(lowercase.Name)
	if err != nil {
		return nil, err
	}
	stopEnFilter, err := cache.TokenFilterNamed(de.StopName)
	if err != nil {
		return nil, err
	}
	rv := analysis.DefaultAnalyzer{
		Tokenizer: tokenizer,
		TokenFilters: []analysis.TokenFilter{
			toLowerFilter,
			stopEnFilter,
		},
	}
	return &rv, nil
}

const deHTML = "de_html"

func deHTMLAnalyzerConstructor(
	config map[string]interface{},
	cache *registry.Cache,
) (analysis.Analyzer, error) {

	htmlFilter, err := cache.CharFilterNamed(bleveHtml.Name)
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
		CharFilters: []analysis.CharFilter{
			htmlFilter,
			&specialCharFilter{},
		},
		Tokenizer: unicodeTokenizer,
		TokenFilters: []analysis.TokenFilter{
			toLowerFilter,
			stopDeFilter,
			normalizeDeFilter,
			lightStemmerDeFilter,
		},
	}
	return &rv, nil
}

type specialCharFilter struct{}

func (f *specialCharFilter) Filter(input []byte) []byte {
	input = []byte(html.UnescapeString(string(input)))
	return input
}

func init() {
	registry.RegisterAnalyzer(deHTML, deHTMLAnalyzerConstructor)
	registry.RegisterAnalyzer(deStandard, deStandardAnalyzerConstructor)
}

type bleveType map[string]any

func newBleveType(typ string) bleveType {
	return bleveType{"_bleve_type": typ}
}

func (bt bleveType) BleveType() string {
	return bt["_bleve_type"].(string)
}

func buildIndexMapping(collections meta.Collections) mapping.IndexMapping {

	numberFieldMapping := bleve.NewNumericFieldMapping()
	textFieldMapping := bleve.NewTextFieldMapping()
	textFieldMapping.Analyzer = de.AnalyzerName

	htmlFieldMapping := bleve.NewTextFieldMapping()
	htmlFieldMapping.Analyzer = deHTML

	stringFieldMapping := bleve.NewTextFieldMapping()
	stringFieldMapping.Analyzer = deStandard

	indexMapping := mapping.NewIndexMapping()
	indexMapping.TypeField = "_bleve_type"

	for name, col := range collections {
		docMapping := bleve.NewDocumentMapping()
		for fname, cf := range col.Fields {
			if cf.Searchable {
				switch cf.Type {
				case "HTMLStrict", "HTMLPermissive":
					docMapping.AddFieldMappingsAt(fname, htmlFieldMapping)
				case "string", "text":
					docMapping.AddFieldMappingsAt(fname, textFieldMapping)
				case "generic-relation":
					docMapping.AddFieldMappingsAt(fname, stringFieldMapping)
				case "relation", "number":
					docMapping.AddFieldMappingsAt(fname, numberFieldMapping)
				case "number[]":
					docMapping.AddFieldMappingsAt(fname, numberFieldMapping)
				default:
					log.Errorf("unsupport type %q on field %s\n", cf.Type, fname)
				}
			}
		}
		indexMapping.AddDocumentMapping(name, docMapping)
	}

	indexMapping.DefaultAnalyzer = de.AnalyzerName

	return indexMapping
}

func (bt bleveType) fill(fields map[string]*meta.Member, data []byte) {
	for fname := range fields {
		switch fields[fname].Type {
		case "HTMLStrict", "HTMLPermissive", "string", "text", "generic-relation":
			if v, err := jsonparser.GetString(data, fname); err == nil {
				bt[fname] = v
				continue
			}
		case "relation", "number":
			if v, err := jsonparser.GetInt(data, fname); err == nil {
				bt[fname] = v
				continue
			}
		case "number[]":
			bt[fname] = []int64{}
			jsonparser.ArrayEach(data, func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
				if v, err := jsonparser.GetInt(value); err == nil {
					bt[fname] = append(bt[fname].([]int64), v)
				}
			}, fname)
			continue
		default:
			if v, _, _, err := jsonparser.Get(data, fname); err == nil {
				bt[fname] = v
				continue
			}
		}

		delete(bt, fname)
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
		log.Infof("building initial text index took %v\n", time.Since(start))
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

func newNumericQuery(num float64) *query.NumericRangeQuery {
	inclusive := true
	numericQuery := bleve.NewNumericRangeQuery(&num, &num)
	numericQuery.InclusiveMin = &inclusive
	numericQuery.InclusiveMax = &inclusive
	return numericQuery
}

// Answer contains additional information of an search results answer
type Answer struct {
	Score        float64
	MatchedWords map[string][]string
}

// Search queries the internal index for hits.
func (ti *TextIndex) Search(question string, meetingID int) (map[string]Answer, error) {
	start := time.Now()
	defer func() {
		log.Debugf("searching for %q took %v\n", question, time.Since(start))
	}()

	var q query.Query
	matchQuery := bleve.NewQueryStringQuery(question)

	if meetingID > 0 {
		fmid := float64(meetingID)
		meetingIDQuery := newNumericQuery(fmid)
		meetingIDQuery.SetField("meeting_id")

		meetingIDsQuery := newNumericQuery(fmid)
		meetingIDsQuery.SetField("meeting_ids")

		meetingIDOwnerQuery := bleve.NewMatchQuery("meeting/" + strconv.Itoa(meetingID))
		meetingIDOwnerQuery.SetField("owner_id")

		meetingQuery := bleve.NewDisjunctionQuery(meetingIDQuery, meetingIDsQuery, meetingIDOwnerQuery)
		q = bleve.NewConjunctionQuery(meetingQuery, matchQuery)
	} else {
		q = matchQuery
	}

	request := bleve.NewSearchRequest(q)
	request.IncludeLocations = true
	result, err := ti.index.Search(request)
	if err != nil {
		return nil, err
	}
	log.Debugf("number hits: %d\n", len(result.Hits))
	dupes := map[string]struct{}{}
	answers := make(map[string]Answer, len(result.Hits))
	numDupes := 0

	for i := range result.Hits {
		fqid := result.Hits[i].ID
		if _, ok := dupes[fqid]; ok {
			numDupes++
			continue
		}

		matchedWords := map[string][]string{}
		for location := range result.Hits[i].Locations {
			matchedWords[location] = []string{}
			for word := range result.Hits[i].Locations[location] {
				matchedWords[location] = append(matchedWords[location], word)
			}
		}

		dupes[fqid] = struct{}{}
		answers[fqid] = Answer{
			Score:        result.Hits[i].Score,
			MatchedWords: matchedWords,
		}
	}
	log.Debugf("number of duplicates: %d\n", numDupes)
	return answers, nil
}
