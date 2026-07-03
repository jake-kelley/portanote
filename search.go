package main

import (
	"math"
	"sort"
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

// SuggestTags proposes topic tags for a note from its TITLE and MARKDOWN
// HEADERS only — never paragraph or code-block content. Candidate terms are
// ranked by TF-IDF (term frequency within title+headers × the term's rarity
// across the whole collection). It runs entirely offline; no note content ever
// leaves the machine. Terms already used as tags, stopwords, and numbers are
// excluded.
func (s *Store) SuggestTags(id string, max int) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.notes[id]
	if !ok {
		return nil
	}

	// candidate pool: title + header text (headers weighted a touch below title)
	tf := map[string]float64{}
	for _, tok := range tokenize(n.Title) {
		tf[tok] += 2
	}
	for _, h := range headerLines(n.Body) {
		for _, tok := range tokenize(h) {
			tf[tok]++
		}
	}
	if len(tf) == 0 {
		return nil
	}

	have := map[string]bool{}
	for _, t := range n.Tags {
		for _, tok := range tokenize(t) {
			have[tok] = true
		}
	}

	nDocs := float64(len(s.idx.docLen))
	type scored struct {
		term  string
		score float64
	}
	ranked := make([]scored, 0, len(tf))
	for term, freq := range tf {
		if stopwords[term] || have[term] || isNumeric(term) || len(term) < 3 {
			continue
		}
		df := float64(len(s.idx.postings[term]))
		idf := math.Log(1 + (nDocs-df+0.5)/(df+0.5))
		ranked = append(ranked, scored{term, freq * idf})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].term < ranked[j].term
	})
	out := make([]string, 0, max)
	for _, r := range ranked {
		out = append(out, r.term)
		if len(out) >= max {
			break
		}
	}
	return out
}

// headerLines returns the text of each ATX markdown header (# … ######),
// skipping anything inside fenced code blocks so code never contributes tags.
func headerLines(body string) []string {
	var out []string
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(t, "#") {
			continue
		}
		if h := strings.TrimSpace(strings.TrimLeft(t, "#")); h != "" {
			out = append(out, h)
		}
	}
	return out
}

func isNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// common English words that make poor tags
var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "but": true, "not": true,
	"you": true, "all": true, "any": true, "can": true, "her": true, "was": true,
	"one": true, "our": true, "out": true, "day": true, "get": true, "has": true,
	"him": true, "his": true, "how": true, "man": true, "new": true, "now": true,
	"old": true, "see": true, "two": true, "way": true, "who": true, "boy": true,
	"did": true, "its": true, "let": true, "put": true, "say": true, "she": true,
	"too": true, "use": true, "that": true, "this": true, "with": true, "have": true,
	"from": true, "they": true, "will": true, "would": true, "there": true, "their": true,
	"what": true, "about": true, "which": true, "when": true, "your": true, "them": true,
	"then": true, "than": true, "some": true, "into": true, "just": true, "over": true,
	"also": true, "such": true, "only": true, "other": true, "these": true, "were": true,
	"been": true, "being": true, "using": true, "used": true, "make": true, "made": true,
	"more": true, "most": true, "each": true, "here": true, "very": true, "should": true,
	"could": true, "because": true, "while": true, "where": true, "after": true, "before": true,
	"against": true, "between": true, "through": true, "during": true, "another": true,
}
