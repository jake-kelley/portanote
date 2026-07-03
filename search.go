package main

import (
	"math"
	"strings"
	"unicode"
)

// Index is an in-memory inverted index with BM25 ranking and
// prefix expansion on the final query token (search-as-you-type).
// Rebuilding happens incrementally on every save; for a personal
// note collection (even tens of thousands of notes) this is instant.
type Index struct {
	postings map[string]map[string]float64 // term -> docID -> weighted term frequency
	docTerms map[string]map[string]float64 // docID -> its terms (for removal)
	docLen   map[string]float64
	totalLen float64
}

func NewIndex() *Index {
	return &Index{
		postings: map[string]map[string]float64{},
		docTerms: map[string]map[string]float64{},
		docLen:   map[string]float64{},
	}
}

// Field weights: a hit in the title or a tag matters far more than one in the body.
const (
	wTitle = 4.0
	wTag   = 6.0
	wBody  = 1.0
)

func (ix *Index) Put(id, title string, tags []string, body string) {
	ix.Remove(id)
	terms := map[string]float64{}
	for _, t := range tokenize(title) {
		terms[t] += wTitle
	}
	for _, tag := range tags {
		for _, t := range tokenize(tag) {
			terms[t] += wTag
		}
	}
	for _, t := range tokenize(body) {
		terms[t] += wBody
	}
	var dl float64
	for t, w := range terms {
		m, ok := ix.postings[t]
		if !ok {
			m = map[string]float64{}
			ix.postings[t] = m
		}
		m[id] = w
		dl += w
	}
	ix.docTerms[id] = terms
	ix.docLen[id] = dl
	ix.totalLen += dl
}

func (ix *Index) Remove(id string) {
	terms, ok := ix.docTerms[id]
	if !ok {
		return
	}
	for t := range terms {
		delete(ix.postings[t], id)
		if len(ix.postings[t]) == 0 {
			delete(ix.postings, t)
		}
	}
	ix.totalLen -= ix.docLen[id]
	delete(ix.docTerms, id)
	delete(ix.docLen, id)
}

const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

func (ix *Index) Search(q string) map[string]float64 {
	scores := map[string]float64{}
	qTerms := tokenize(q)
	n := float64(len(ix.docLen))
	if n == 0 || len(qTerms) == 0 {
		return scores
	}
	avgdl := ix.totalLen / n

	for i, term := range qTerms {
		// the token still being typed also matches as a prefix
		expansions := [][2]interface{}{}
		if m, ok := ix.postings[term]; ok {
			expansions = append(expansions, [2]interface{}{term, m})
		}
		if i == len(qTerms)-1 {
			count := 0
			for vocab, m := range ix.postings {
				if vocab != term && strings.HasPrefix(vocab, term) {
					expansions = append(expansions, [2]interface{}{vocab, m})
					if count++; count >= 40 {
						break
					}
				}
			}
		}
		for _, ex := range expansions {
			m := ex[1].(map[string]float64)
			df := float64(len(m))
			idf := math.Log(1 + (n-df+0.5)/(df+0.5))
			for id, tf := range m {
				dl := ix.docLen[id]
				scores[id] += idf * tf * (bm25K1 + 1) / (tf + bm25K1*(1-bm25B+bm25B*dl/avgdl))
			}
		}
	}
	return scores
}

func tokenize(s string) []string {
	s = strings.ToLower(s)
	out := []string{}
	var cur strings.Builder
	flush := func() {
		if cur.Len() >= 2 {
			out = append(out, cur.String())
		}
		cur.Reset()
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}
