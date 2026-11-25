package paper

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"textus/backend/ai/aicompat"
)

// A drafted paper reviewed as a whole, before any of it is proposed: a
// reader who sees every section at once reports what only the whole shows
// - repetition, contradictions, a key point no section made, a figure the
// text never mentions, a claim stronger than its notes. Each section with
// points against it is revised once by its writer; a revision that doesn't
// hold leaves the draft as it was.

const reportIssuesTool = "report_issues"

// issue is one point against a section.
type issue struct {
	// section is the section's index in the outline, from 0.
	section      int
	problem, fix string
}

func reportIssuesSpec() aicompat.ToolSpec {
	return aicompat.ToolSpec{
		Name:        reportIssuesTool,
		Description: "Report what needs fixing, once, as a list - empty if nothing does.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"issues": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"section": map[string]any{"type": "integer", "description": "The section's number, from 1"},
							"problem": map[string]any{"type": "string"},
							"fix":     map[string]any{"type": "string", "description": "What its writer should do"},
						},
						"required": []any{"section", "problem", "fix"},
					},
				},
			},
			"required": []any{"issues"},
		},
	}
}

// readIssues is a report_issues call's points, in the reviewer's order, at
// most maxPointsPerSection a section; or why it can't be read.
func readIssues(arguments string, sections int) ([]issue, error) {
	args, err := parseValue(arguments)
	if err != nil {
		return nil, err
	}
	items, ok := args["issues"].([]any)
	if !ok {
		return nil, errors.New("Pass `issues`, a list (empty if nothing needs fixing).")
	}
	out := []issue{}
	for _, item := range items {
		o, _ := item.(object)
		number, _ := wholeNumber(o["section"])
		if number == 0 || number > uint64(sections) {
			return nil, fmt.Errorf("There is no section %d: they are numbered 1 to %d.", number, sections)
		}
		problem, fix := strings.TrimSpace(str(o, "problem")), strings.TrimSpace(str(o, "fix"))
		index := int(number) - 1
		against := 0
		for _, i := range out {
			if i.section == index {
				against++
			}
		}
		if problem == "" || against >= maxPointsPerSection {
			continue
		}
		out = append(out, issue{section: index, problem: problem, fix: fix})
	}
	return out, nil
}

// reviewInstructions is what the reviewer is told.
func reviewInstructions(paper *Paper) string {
	profile := paper.Genre.Profile()
	return fill("review.instructions", kv{"genre", profile.Name}, kv{"language", paper.Language}, kv{"review_for", profile.ReviewFor})
}

// reviewMessage is the reviewer's message: the thesis, the outline and the
// drafts.
func reviewMessage(outline *Outline, sections []string) string {
	outlineText := make([]string, len(outline.Sections))
	for i, s := range outline.Sections {
		outlineText[i] = fmt.Sprintf("%d. %s - %s", i+1, s.Title, strings.Join(s.KeyPoints, "; "))
	}
	drafts := make([]string, len(sections))
	for i, s := range sections {
		drafts[i] = fmt.Sprintf("<section number=\"%d\">\n%s\n</section>", i+1, s)
	}
	return fill("review.message", kv{"thesis", outline.Thesis}, kv{"outline", strings.Join(outlineText, "\n")},
		kv{"drafts", strings.Join(drafts, "\n\n")})
}

// review is the points against the drafted sections (each under its
// heading). A reviewer that reports nothing it can read has no points: the
// drafts passed their own checks.
func review(ctx context.Context, model Model, paper *Paper, outline *Outline, sections []string) ([]issue, error) {
	t := task{instructions: reviewInstructions(paper), message: reviewMessage(outline, sections), tool: reportIssuesSpec(), steps: reviewSteps}
	issues, _, err := handOver(ctx, model, t, func(arguments string) checked[[]issue] {
		read, err := readIssues(arguments, len(sections))
		if err != nil {
			return refused[[]issue](fmt.Sprintf("Not read: %v Report again.", err))
		}
		return keptAs(read)
	})
	if err != nil || issues == nil {
		return nil, err
	}
	return *issues, nil
}

// reviewAll is the points against the drafted sections: the review's,
// and with checkClaims those of the check of every cited sentence against
// its notes, run at once. A review that fails leaves the check's.
func reviewAll(ctx context.Context, model Model, paper *Paper, outline *Outline, dossier *Dossier, sections []string, checkClaims bool) []issue {
	var checked []issue
	var wg sync.WaitGroup
	if checkClaims {
		wg.Go(func() { checked = checkAllClaims(ctx, model, paper, dossier, sections) })
	}
	reviewed, _ := review(ctx, model, paper, outline, sections)
	wg.Wait()
	return append(checked, reviewed...)
}

// revise is sections with each one issues names revised once, as many at a
// time as the model takes. revising hears of each as it begins; a revision
// that fails leaves the draft. Returns which sections changed, in order.
func revise(ctx context.Context, model Model, paper *Paper, outline *Outline, dossier *Dossier, sections []string, issues []issue, revising func(int), send Send, draw Draw) []int {
	var named []int
	for _, i := range issues {
		named = append(named, i.section)
	}
	slices.Sort(named)
	named = slices.Compact(named)
	drafts := slices.Clone(sections)
	revised := make([]*string, len(sections))
	slots := make(chan struct{}, max(model.AtOnce(), 1))
	var wg sync.WaitGroup
	for _, i := range named {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			revising(i)
			var points []string
			for _, x := range issues {
				if x.section == i {
					points = append(points, x.problem+" - "+x.fix)
				}
			}
			// The draft without its heading, as the writer handed it over.
			body := drafts[i]
			if paper.Genre.Form().Headings {
				_, body, _ = strings.Cut(drafts[i], "\n\n")
			}
			text, err := writeSection(ctx, model, paper, outline, i, dossier, &revision{earlier: body, points: points}, send, draw)
			if err == nil {
				revised[i] = &text
			}
		})
	}
	wg.Wait()
	var changed []int
	for i, text := range revised {
		if text != nil {
			sections[i] = *text
			changed = append(changed, i)
		}
	}
	return changed
}
