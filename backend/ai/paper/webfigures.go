package paper

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
	"textus/backend/ai/web"
)

// A paper's web pictures: each `<!-- textus-figure web: caption | search
// words -->` mark the writers left becomes the best match on Wikimedia
// Commons, kept in the picture store like a generated picture - shown in
// place, saved with the paper - with the credit its licence asks for on
// the line after it. A mark nothing is found for becomes a picture for the
// image model to make instead: a real picture first, a generated one only
// where none is found.

// readMark is a mark's caption and the words to search for (the caption
// itself when the mark gives none); false for any other line.
func readMark(line string) (caption, words string, ok bool) {
	body, ok := strings.CutPrefix(strings.TrimSpace(line), WebFigureMark)
	if !ok {
		return "", "", false
	}
	body, ok = strings.CutSuffix(strings.TrimRightFunc(body, unicode.IsSpace), "-->")
	if !ok {
		return "", "", false
	}
	body = strings.TrimSpace(body)
	caption, words = body, body
	if c, w, cut := strings.Cut(body, "|"); cut && strings.TrimSpace(w) != "" {
		caption, words = strings.TrimSpace(c), strings.TrimSpace(w)
	}
	return caption, words, caption != ""
}

// webPicturesAtOnce are the marks searched for at once, across all a
// paper's sections: each search and fetch is mostly waiting on Commons,
// and a few at a time stay polite.
const webPicturesAtOnce = 4

// FindPicture is the picture for search words and its credit, or why
// there is none.
type FindPicture func(ctx context.Context, words string) (aicompat.GeneratedImage, string, error)

// found is a web picture kept in the store: its id and credit.
type found struct{ id, credit string }

// ResolveWebPictures is sections with their web-picture marks replaced by
// the pictures find finds - the picture and its credit - each kept in the
// picture store. A mark find finds nothing for becomes a textus-picture:
// line, begun with the planned generated pictures, or left out with them
// when there is no image model. The marks are searched for a few at a
// time, whichever section they are in, and each lands where its mark was.
func ResolveWebPictures(ctx context.Context, sections []string, find FindPicture) []string {
	var searches []string
	for _, s := range sections {
		for _, line := range strings.Split(s, "\n") {
			if _, words, ok := readMark(line); ok {
				searches = append(searches, words)
			}
		}
	}
	results := make([]*found, len(searches))
	slots := make(chan struct{}, webPicturesAtOnce)
	var wg sync.WaitGroup
	for i, words := range searches {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			image, credit, err := find(ctx, words)
			if err != nil {
				return
			}
			// Kept in the store as soon as found, so the bytes aren't held
			// here while the slower searches finish.
			results[i] = &found{ai.KeepPicture(image), credit}
		})
	}
	wg.Wait()
	out := make([]string, len(sections))
	for i, s := range sections {
		out[i] = placeWebPictures(s, &results)
	}
	return out
}

// placeWebPictures is section with each of its marks replaced by the next
// of results: the kept picture and its credit, or nil for one to generate
// instead.
func placeWebPictures(section string, results *[]*found) string {
	if !strings.Contains(section, WebFigureMark) {
		return section
	}
	var out []string
	for _, line := range strings.Split(section, "\n") {
		caption, words, ok := readMark(line)
		if !ok {
			out = append(out, line)
			continue
		}
		var next *found
		if len(*results) > 0 {
			next, *results = (*results)[0], (*results)[1:]
		}
		if next == nil {
			out = append(out, generatedInstead(caption, words))
			continue
		}
		alt := strings.NewReplacer("[", "", "]", "").Replace(caption)
		out = append(out, fmt.Sprintf("![%s](%s)", alt, ai.PlaceholderSrc(next.id)), "", "*"+next.credit+"*")
	}
	return closeGaps(strings.Join(out, "\n"))
}

// generatedInstead is the picture line a mark Commons has nothing for
// becomes: its caption as the alt text, and as the prompt the caption with
// the search words, which say in English what it shows.
func generatedInstead(caption, words string) string {
	alt := strings.NewReplacer("[", "", "]", "").Replace(caption)
	if words == caption {
		return fmt.Sprintf("![%s](textus-picture:16:9 %s)", alt, alt)
	}
	return fmt.Sprintf("![%s](textus-picture:16:9 %s (%s))", alt, alt, words)
}

// fewerWords are words, then with its last word dropped, then its last
// two, keeping two at least: a search too narrow to find anything gets a
// wider one.
func fewerWords(words string) []string {
	all := strings.Fields(words)
	tries := []string{strings.Join(all, " ")}
	for _, n := range []int{max(len(all)-1, 0), max(len(all)-2, 0)} {
		shorter := strings.Join(all[:n], " ")
		if n >= 2 && !slices.Contains(tries, shorter) {
			tries = append(tries, shorter)
		}
	}
	return tries
}

// FindOnCommons is the best picture on Commons for words that can be
// fetched, and its credit; with nothing found, the same search with fewer
// words.
func FindOnCommons(ctx context.Context, words string) (aicompat.GeneratedImage, string, error) {
	last := fmt.Errorf("Nothing on Wikimedia Commons for %s.", debugQuoted(words))
	for _, query := range fewerWords(words) {
		images, err := web.CommonsSearch(ctx, query)
		if err != nil {
			return aicompat.GeneratedImage{}, "", err
		}
		for _, image := range images[:min(len(images), commonsTries)] {
			picture, err := web.CommonsFetch(ctx, image)
			if err == nil {
				return picture, image.Credit(), nil
			}
			last = err
		}
	}
	return aicompat.GeneratedImage{}, "", last
}
