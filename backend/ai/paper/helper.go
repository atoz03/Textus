package paper

import (
	"context"
	"fmt"
	"sync"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
	"textus/backend/ai/web"
)

// One question's research: its helper, searching and keeping notes with
// add_note, and - when the user lets it - the helpers it hands narrower
// parts of its question to with delegate_task.

// helperInstructions is what a question's helper is told; own is what it
// is told about the user's own material, and delegates whether it may hand
// parts over.
func helperInstructions(paper *Paper, own string, delegates bool) string {
	profile := paper.Genre.Profile()
	handing := ""
	if delegates {
		handing = fill("research.delegate", kv{"tool", delegateTool}) + " "
	}
	return fill("research.helper", kv{"genre", profile.Name}, kv{"topic", paper.Topic}, kv{"language", paper.Language},
		kv{"sources", profile.ResearchSources}, kv{"delegate", handing}, kv{"material", own})
}

// partInstructions is what the helper of a part handed over is told: of is
// the question the part belongs to.
func partInstructions(paper *Paper, own, of string) string {
	profile := paper.Genre.Profile()
	return fill("research.part", kv{"genre", profile.Name}, kv{"topic", paper.Topic}, kv{"language", paper.Language},
		kv{"of", of}, kv{"sources", profile.ResearchSources}, kv{"material", own})
}

// researchEnv is what every helper of the paper works with.
type researchEnv struct {
	model  Model
	paper  *Paper
	plan   *web.Plan
	budget *SharedBudget
	kit    *Kit
	send   Send
}

// desk is what one question's helper and the helpers it hands parts to
// share: the question's notes, and its share of the budget and what of it
// is spent.
type desk struct {
	index uint32
	share share
	mu    sync.Mutex
	spent [3]int
	notes []Note
	// handed are the parts handed over so far.
	handed int
	// delegates: the question's helper may hand parts over - the user's
	// choice.
	delegates bool
}

// researchQuestion is one question's helper: its notes, in the order it
// kept them - with those of the helpers it handed parts to.
func researchQuestion(ctx context.Context, model Model, paper *Paper, plan *web.Plan, budget *SharedBudget, kit *Kit, index uint32, question string, share share, send Send) ([]Note, error) {
	d := &desk{index: index, share: share, delegates: paper.SubHelpers}
	env := &researchEnv{model, paper, plan, budget, kit, send}
	if err := env.helper(ctx, d, question, nil); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.notes, nil
}

// part is a part handed over, with the question it belongs to; nil for a
// question's own helper.
type part struct {
	of     string
	handed handed
}

// helper is one helper's run: its own registry has the web tools, ranked
// by its question, and add_note - and delegate_task for a question's own
// helper; the rest it shares with the other helpers through env.kit. What
// it keeps goes on d.
func (env *researchEnv) helper(ctx context.Context, d *desk, question string, role *part) error {
	kit := env.kit
	instructions, first, steps := helperInstructions(env.paper, kit.prompt(), d.delegates), "Your question: "+question, helperSteps
	if role != nil {
		instructions, first, steps = partInstructions(env.paper, kit.prompt(), role.of), brief(role.handed), subSteps
	}
	taker := &noteTaker{question: question, index: d.index, plan: env.plan, kit: kit, desk: d, send: env.send, sentBack: map[string]bool{}}
	own := ai.SharedToolRegistry()
	for _, t := range env.plan.Tools(question) {
		own.Add(t)
	}
	own.Add(taker.tool())
	specs := own.Specs()
	delegates := d.delegates && role == nil
	if delegates {
		specs = append(specs, delegateSpec())
	}
	specs = append(specs, kit.Tools.Specs()...)
	execute := func(call aicompat.ToolCall) aicompat.ToolOutput {
		if delegates && call.Name == delegateTool {
			return aicompat.Text(env.handOver(ctx, d, question, call))
		}
		d.mu.Lock()
		refused := d.share.spend(call.Name, &d.spent)
		d.mu.Unlock()
		if refused != "" {
			return aicompat.Text(refused)
		}
		webCall := call.Name != addNoteTool && own.Offers(call.Name)
		if webCall || kit.Tools.Offers(call.Name) {
			env.send(ActivityEvent{Question: d.index, Tool: call.Name, Arguments: call.Arguments})
		}
		if !own.Offers(call.Name) {
			return kit.Tools.Execute(kit.Ctx, call)
		}
		output := own.Execute(kit.Ctx, call)
		if webCall {
			env.send(budgetEvent(env.budget))
		}
		return output
	}
	_, err := aicompat.Loop{
		MaxSteps: steps,
		Step: func(turns []aicompat.AgentTurn) (aicompat.StepResult, error) {
			return env.model.Step(ctx, instructions, turns, specs, nil)
		},
		Execute: execute,
		HowToRun: func(name string) aicompat.ToolRun {
			if own.Offers(name) {
				return own.HowToRun(name)
			}
			// delegate_task, too: one helper at a time, in call order.
			return kit.Tools.HowToRun(name)
		},
	}.Run(nil, aicompat.UserTurn(first))
	return err
}

// handOver runs a delegate_task call: the part handed to a helper of its
// own, and what it kept told back - or why it wasn't handed over. A
// helper that fails costs the part, not the question.
func (env *researchEnv) handOver(ctx context.Context, d *desk, question string, call aicompat.ToolCall) string {
	h, why := readHanded(call.Arguments, question)
	if why != "" {
		return why
	}
	d.mu.Lock()
	if d.share.usedUp(d.spent) {
		d.mu.Unlock()
		return "Your question's share of searches and page reads is used up: there is nothing left for a helper to search with. Keep notes from what you have."
	}
	if d.handed >= maxHanded {
		d.mu.Unlock()
		return fmt.Sprintf("You have handed over %d parts already: research the rest yourself.", maxHanded)
	}
	d.handed++
	before := len(d.notes)
	d.mu.Unlock()
	env.send(ActivityEvent{Question: d.index, Tool: delegateTool, Arguments: call.Arguments})
	err := env.helper(ctx, d, h.question, &part{of: question, handed: h})
	d.mu.Lock()
	kept := append([]Note(nil), d.notes[before:]...)
	d.mu.Unlock()
	if err != nil && len(kept) == 0 {
		return "The helper stopped before keeping anything: " + err.Error() + ". Cover the part yourself if it matters."
	}
	return answer(h.question, kept)
}
