package main

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The half-typed last token expands against the vocabulary. That expansion is
// capped, so when more words match than the cap allows, the cap has to choose —
// and it must choose the same way every time. It used to range over the
// postings map, which Go deliberately randomizes, so identical queries returned
// different results run to run once the prefix matched more than the cap.
func TestPrefixExpansionIsDeterministic(t *testing.T) {
	ix := NewIndex()
	for i := 0; i < prefixExpansionMax*3; i++ {
		ix.Put(fmt.Sprintf("n%03d", i), fmt.Sprintf("condition%03d", i), nil, "body text")
	}

	first := ix.Search("cond")
	if len(first) == 0 {
		t.Fatal("prefix search matched nothing")
	}
	for i := 0; i < 20; i++ {
		if got := ix.Search("cond"); !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d returned a different result set for the same query", i)
		}
	}
}

// Over the cap, the widest-used words are the ones kept — ties alphabetical.
func TestPrefixExpansionPrefersCommonTerms(t *testing.T) {
	ix := NewIndex()
	// "alpha000" appears in many notes, the rest in one each
	for i := 0; i < prefixExpansionMax*2; i++ {
		ix.Put(fmt.Sprintf("n%03d", i), "alpha000", nil, fmt.Sprintf("alpha%03d", i))
	}

	hits := ix.prefixMatches("alpha")
	if len(hits) != prefixExpansionMax {
		t.Fatalf("expansions = %d, want the cap %d", len(hits), prefixExpansionMax)
	}
	if hits[0] != "alpha000" {
		t.Errorf("most common term ranked %q first, want alpha000", hits[0])
	}
}

// An exact hit is scored by the caller, so the expansion list must not repeat
// it and double-count its weight.
func TestPrefixExpansionExcludesExactTerm(t *testing.T) {
	ix := NewIndex()
	ix.Put("a", "note", nil, "notebook notepad")

	for _, got := range ix.prefixMatches("note") {
		if got == "note" {
			t.Fatal("exact term appeared among its own prefix expansions")
		}
	}
}

// The sorted vocabulary is a second copy of the postings keys; if the two ever
// drift, prefix search silently starts missing (or inventing) words.
func TestVocabularyTracksPostings(t *testing.T) {
	ix := NewIndex()
	ix.Put("a", "alpha beta", nil, "gamma")
	ix.Put("b", "beta delta", nil, "gamma")
	ix.Put("a", "alpha", nil, "") // rewrite drops beta and gamma from a
	ix.Remove("b")

	want := make([]string, 0, len(ix.postings))
	for term := range ix.postings {
		want = append(want, term)
	}
	sort.Strings(want)

	if !reflect.DeepEqual(ix.terms, want) {
		t.Errorf("vocabulary = %v, postings keys = %v", ix.terms, want)
	}
	if !sort.StringsAreSorted(ix.terms) {
		t.Errorf("vocabulary is not sorted: %v", ix.terms)
	}
}

// Prefix expansion only applies to the token being typed — earlier tokens in
// the query are complete words and must match exactly.
func TestPrefixExpansionOnlyOnLastToken(t *testing.T) {
	ix := NewIndex()
	ix.Put("match", "release notes", nil, "")
	ix.Put("prefixOnly", "released", nil, "") // reachable only if "release" expands

	scores := ix.Search("release not")
	if scores["match"] <= 0 {
		t.Fatal("exact first token plus prefixed second token did not match")
	}
	if scores["prefixOnly"] > 0 {
		t.Error("a non-final token expanded as a prefix")
	}
}

// Two notes saved in the same instant that score identically still have to come
// back in the same order twice.
func TestSearchResultOrderIsStable(t *testing.T) {
	s, _ := newTestStore(t)
	for i := 0; i < 12; i++ {
		n, err := s.Create("duplicate subject")
		if err != nil {
			t.Fatal(err)
		}
		mustUpdate(t, s, n.ID, UpdateReq{Body: strp("same body")})
	}

	ids := func() string {
		var b strings.Builder
		for _, r := range s.Search("duplicate", false) {
			b.WriteString(r.ID + " ")
		}
		return b.String()
	}
	first := ids()
	for i := 0; i < 20; i++ {
		if got := ids(); got != first {
			t.Fatalf("run %d ordered identical-scoring results differently", i)
		}
	}
}
