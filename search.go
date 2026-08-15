package main

import (
	"math"
	"slices"
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
	terms    []string // vocabulary, sorted — prefix lookup without scanning postings
}

func NewIndex() *Index {
	return &Index{
		postings: map[string]map[string]float64{},
		docTerms: map[string]map[string]float64{},
		docLen:   map[string]float64{},
	}
}

// addTerm/dropTerm keep ix.terms sorted alongside ix.postings. Vocabulary
// churns far less than the postings do — only a genuinely new word inserts,
// and only the last document to use a word removes it — so the O(n) shift is
// paid rarely, in exchange for prefix lookups that never scan the vocabulary.
func (ix *Index) addTerm(t string) {
	i := sort.SearchStrings(ix.terms, t)
	ix.terms = slices.Insert(ix.terms, i, t)
}

func (ix *Index) dropTerm(t string) {
	i := sort.SearchStrings(ix.terms, t)
	if i < len(ix.terms) && ix.terms[i] == t {
		ix.terms = slices.Delete(ix.terms, i, i+1)
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
			ix.addTerm(t)
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
			ix.dropTerm(t)
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
		var expansions []string
		if _, ok := ix.postings[term]; ok {
			expansions = append(expansions, term)
		}
		if i == len(qTerms)-1 {
			expansions = append(expansions, ix.prefixMatches(term)...)
		}
		for _, ex := range expansions {
			m := ix.postings[ex]
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

// how many vocabulary words one half-typed token may expand to
const prefixExpansionMax = 40

// prefixMatches returns vocabulary terms beginning with prefix, excluding an
// exact match (the caller scores that separately). Binary search finds the
// range, so cost scales with the number of matches rather than the size of the
// vocabulary.
//
// When more than prefixExpansionMax match, the most widely used words win, ties
// broken alphabetically. Any rule would do; having one is the point. This
// previously ranged over the postings map and stopped at 40, and Go randomizes
// map order — so the same query could return different results each time it ran.
func (ix *Index) prefixMatches(prefix string) []string {
	var hits []string
	for _, t := range ix.terms[sort.SearchStrings(ix.terms, prefix):] {
		if !strings.HasPrefix(t, prefix) {
			break
		}
		if t != prefix {
			hits = append(hits, t)
		}
	}
	if len(hits) <= prefixExpansionMax {
		return hits
	}
	sort.SliceStable(hits, func(a, b int) bool {
		return len(ix.postings[hits[a]]) > len(ix.postings[hits[b]])
	})
	return hits[:prefixExpansionMax]
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
