package paper

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
)

// Finishing a paper a few lookups at a time: the same paper as one at a
// time, sooner. The clock is a bubble's, so a lookup's wait costs no real
// time and the times are what the waits add up to.

func TestAWebPictureMarkGivesItsCaptionAndSearchWords(t *testing.T) {
	for _, c := range []struct {
		line, caption, words string
		ok                   bool
	}{
		{"<!-- textus-figure web: 苏黎世的屋顶绿化 | green roof Zurich -->", "苏黎世的屋顶绿化", "green roof Zurich", true},
		{"  <!-- textus-figure web: Basel rooftops -->", "Basel rooftops", "Basel rooftops", true},
		{"<!-- textus-figure web:  -->", "", "", false},
		{"Plain text.", "", "", false},
	} {
		caption, words, ok := readMark(c.line)
		if ok != c.ok || ok && (caption != c.caption || words != c.words) {
			t.Errorf("%s: %q %q %t", c.line, caption, words, ok)
		}
	}
}

func TestAWebPictureSearchWidensByDroppingItsLastWords(t *testing.T) {
	for words, want := range map[string][]string{
		"green roof Zurich city": {"green roof Zurich city", "green roof Zurich", "green roof"},
		"green roof Zurich":      {"green roof Zurich", "green roof"},
		"Basel rooftops":         {"Basel rooftops"},
	} {
		if got := fewerWords(words); !slices.Equal(got, want) {
			t.Errorf("%s: %q", words, got)
		}
	}
}

func TestWebPictureMarksBecomeKeptPicturesWithCreditsOrGeneratedOnes(t *testing.T) {
	section := "Intro.\n\n<!-- textus-figure web: A roof in Basel | green roof Basel -->\n\nMid.\n\n<!-- textus-figure web: Nothing | no such picture -->\n\nEnd."
	credit := "Ann · CC BY 4.0 · [Wikimedia Commons](https://commons.wikimedia.org/wiki/File:R.jpg)"
	find := func(_ context.Context, words string) (aicompat.GeneratedImage, string, error) {
		if words == "green roof Basel" {
			return aicompat.GeneratedImage{Bytes: []byte{1, 2, 3}, Mime: "image/jpeg", Ext: "jpg", Width: 4, Height: 3}, credit, nil
		}
		return aicompat.GeneratedImage{}, "", errors.New("nothing")
	}
	resolved := ResolveWebPictures(t.Context(), []string{section}, find)[0]
	lines := strings.Split(resolved, "\n\n")
	if len(lines) != 6 || lines[0] != "Intro." {
		t.Fatal(resolved)
	}
	id, ok := strings.CutPrefix(lines[1], "![A roof in Basel](textus-generated:")
	id, closed := strings.CutSuffix(id, ")")
	if !ok || !closed {
		t.Fatal(resolved)
	}
	if lines[2] != "*"+credit+"*" {
		t.Fatal(lines[2])
	}
	// Commons has nothing: a picture for the image model to make instead.
	if want := []string{"Mid.", "![Nothing](textus-picture:16:9 Nothing (no such picture))", "End."}; !slices.Equal(lines[3:], want) {
		t.Fatal(lines[3:])
	}
	if bytes, ext, ok := ai.ReadyPicture(id); !ok || !slices.Equal(bytes, []byte{1, 2, 3}) || ext != "jpg" {
		t.Fatal(bytes, ext, ok)
	}

	// Numbered like any picture, the credit after its caption.
	sections := []string{resolved}
	NumberFigures(sections, "en")
	if !strings.Contains(sections[0], ")\n\n*Figure 1. A roof in Basel*\n\n*Ann · CC BY 4.0") {
		t.Fatal(sections[0])
	}
}

// scattered is how long lookup i takes: between 100 ms and a second, the
// same every run, so later lookups often end before earlier ones.
func scattered(i int) time.Duration {
	return time.Duration(i*7919%10+1) * 100 * time.Millisecond
}

// inFlight counts how many lookups wait at once.
type inFlight struct{ now, most atomic.Int64 }

func (f *inFlight) wait(d time.Duration) {
	now := f.now.Add(1)
	for most := f.most.Load(); now > most && !f.most.CompareAndSwap(most, now); most = f.most.Load() {
	}
	time.Sleep(d)
	f.now.Add(-1)
}

// addressed is a dossier of n sources cited by their address alone, but
// every fifth already titled.
func addressed(n int) Dossier {
	var d Dossier
	for i := range n {
		s := aicompat.WebSource{N: uint32(i + 1), URL: fmt.Sprintf("https://example.org/%d", i)}
		if i%5 == 4 {
			s.Title = fmt.Sprintf("Kept %d", i)
		}
		d.Sources = append(d.Sources, s)
	}
	return d
}

func numberIn(address string) int {
	n, err := strconv.Atoi(address[strings.LastIndex(address, "/")+1:])
	if err != nil {
		panic(address)
	}
	return n
}

func TestSourceTitlesLandOnTheirOwnSourcesWhateverOrderLookupsEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var lookups inFlight
		titled := addressed(20)
		// Every third address has no title to find: its source stays
		// untitled.
		TitleSources(t.Context(), &titled, func(_ context.Context, address string) (string, bool) {
			i := numberIn(address)
			lookups.wait(scattered(i))
			return fmt.Sprintf("Title %d", i), i%3 != 0
		})
		for i, s := range titled.Sources {
			want := fmt.Sprintf("Title %d", i)
			switch {
			case i%5 == 4:
				want = fmt.Sprintf("Kept %d", i)
			case i%3 == 0:
				want = ""
			}
			if s.Title != want {
				t.Errorf("source %d: %q, want %q", i, s.Title, want)
			}
		}
		if most := lookups.most.Load(); most != lookupsAtOnce {
			t.Fatal(most)
		}
	})
}

func TestTwentyTitlesLookedUpSixAtATimeTakeAFifthOfTheTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const wait = 300 * time.Millisecond
		lookUp := func(_ context.Context, address string) (string, bool) {
			time.Sleep(wait)
			return address, true
		}
		untitled := addressed(20)
		for i := range untitled.Sources {
			untitled.Sources[i].Title = ""
		}
		// What finishing did before: one lookup after another.
		oneByOne := Dossier{Sources: slices.Clone(untitled.Sources)}
		start := time.Now()
		for i, s := range oneByOne.Sources {
			oneByOne.Sources[i].Title, _ = lookUp(t.Context(), s.URL)
		}
		sequential := time.Since(start)
		atOnce := Dossier{Sources: slices.Clone(untitled.Sources)}
		start = time.Now()
		TitleSources(t.Context(), &atOnce, lookUp)
		concurrent := time.Since(start)
		t.Logf("title sources: 20 × 300ms: sequential %v, concurrent %v", sequential, concurrent)
		sameJSON(t, atOnce, oneByOne)
		// Four rounds of six.
		if sequential != wait*20 || concurrent != wait*4 {
			t.Fatal(sequential, concurrent)
		}
	})
}

// marked is section s with three marks, the last one Commons has nothing
// for.
func marked(s int) string {
	marks := make([]string, 3)
	for m := range marks {
		marks[m] = fmt.Sprintf("<!-- textus-figure web: Picture %d%d | words %d%d -->", s, m, s, m)
	}
	return fmt.Sprintf("Section %d.\n\n%s\n\nEnd %d.", s, strings.Join(marks, "\n\nBetween.\n\n"), s)
}

func picture(n byte) aicompat.GeneratedImage {
	return aicompat.GeneratedImage{Bytes: []byte{n}, Mime: "image/jpeg", Ext: "jpg", Width: 4, Height: 3}
}

func TestWebPicturesLandWhereTheirMarksWereFourSearchesAtATime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var searches inFlight
		sections := []string{marked(0), marked(1), marked(2), marked(3)}
		resolved := ResolveWebPictures(t.Context(), sections, func(_ context.Context, words string) (aicompat.GeneratedImage, string, error) {
			mark := strings.TrimPrefix(words, "words ")
			n, _ := strconv.Atoi(mark)
			searches.wait(scattered(n))
			if n%10 == 2 {
				return aicompat.GeneratedImage{}, "", errors.New("nothing")
			}
			return picture(byte(n)), "Credit " + mark, nil
		})
		if most := searches.most.Load(); most != webPicturesAtOnce {
			t.Fatal(most)
		}
		ids := regexp.MustCompile(`textus-generated:[^)]+`)
		for s, section := range resolved {
			want := fmt.Sprintf("Section %[1]d.\n\n"+
				"![Picture %[1]d0](textus-generated:ID)\n\n*Credit %[1]d0*\n\nBetween.\n\n"+
				"![Picture %[1]d1](textus-generated:ID)\n\n*Credit %[1]d1*\n\nBetween.\n\n"+
				"![Picture %[1]d2](textus-picture:16:9 Picture %[1]d2 (words %[1]d2))\n\nEnd %[1]d.", s)
			if got := ids.ReplaceAllString(section, "textus-generated:ID"); got != want {
				t.Errorf("got  %q\nwant %q", got, want)
			}
		}
	})
}

func TestTwelveWebPicturesSearchedFourAtATimeTakeAQuarterOfTheTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const wait = 300 * time.Millisecond
		find := func(context.Context, string) (aicompat.GeneratedImage, string, error) {
			time.Sleep(wait)
			return picture(0), "Credit", nil
		}
		sections := []string{marked(0), marked(1), marked(2), marked(3)}
		start := time.Now()
		for _, section := range sections {
			for _, line := range strings.Split(section, "\n") {
				if _, words, ok := readMark(line); ok {
					_, _, _ = find(t.Context(), words)
				}
			}
		}
		sequential := time.Since(start)
		start = time.Now()
		ResolveWebPictures(t.Context(), sections, find)
		concurrent := time.Since(start)
		t.Logf("web figures: 12 × 300ms: sequential %v, concurrent %v", sequential, concurrent)
		if sequential != wait*12 || concurrent != wait*3 {
			t.Fatal(sequential, concurrent)
		}
	})
}
