package paper

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strconv"
	"strings"

	"textus/backend/ai"
)

// Which genre a request calls for, and how long: one tool-less step that
// reads the request before any research, so a topic is written as the
// piece it asks for - a paper, a review, a report - without the user
// having to say.

// Choose is the genre and length in characters request calls for. asked
// is the length the user picked (nil for none), kept when it suits the
// genre; a reply that can't be read keeps the paper.
func Choose(ctx context.Context, model Model, request string, asked *uint32) (Genre, uint32) {
	reply, _ := ask(ctx, model, chooseInstructions(asked), "Request: "+request, nil)
	return readChoice(reply, asked)
}

// chooseInstructions is what the choosing step is told: the genres, and
// the length the user picked if any.
func chooseInstructions(asked *uint32) string {
	genres := make([]string, len(Genres))
	for i, g := range Genres {
		p := g.Profile()
		genres[i] = fill("choose.genre", kv{"name", p.Name}, kv{"when", p.When}, kv{"least", p.Chars.least}, kv{"most", p.Chars.most})
	}
	length := prompt("choose.length.open")
	if asked != nil {
		length = fill("choose.length.asked", kv{"asked", *asked})
	}
	return fill("choose.instructions", kv{"genres", strings.Join(genres, "\n")}, kv{"length", length})
}

// readChoice is the genre and length in reply, fitted to the genre's
// lengths.
func readChoice(reply string, asked *uint32) (Genre, uint32) {
	var parsed map[string]jsontext.Value
	ai.JSONObject(reply, &parsed)
	var name string
	genre, found := GenrePaper, false
	if raw, ok := parsed["genre"]; ok && raw.Kind() == '"' {
		_ = json.Unmarshal(raw, &name)
		for _, g := range Genres {
			if g.Profile().Name == name {
				genre, found = g, true
			}
		}
	}
	if !found {
		return GenrePaper, GenrePaper.Length(asked)
	}
	chars := asked
	if raw, ok := parsed["chars"]; ok {
		if n, err := strconv.ParseUint(string(raw), 10, 64); err == nil {
			c := uint32(n)
			chars = &c
		}
	}
	return genre, genre.Length(chars)
}
