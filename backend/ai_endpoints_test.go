package backend

import (
	"fmt"
	"os"
	"sync"
	"testing"

	"textus/backend/ai"
)

func TestSavesAndRemovalsAtOnceKeepEveryChange(t *testing.T) {
	a := &App{dirs: Dirs{Config: t.TempDir()}}
	save := func(i int) {
		key := fmt.Sprintf("sk-%d", i)
		edit := ai.EndpointEdit{ID: fmt.Sprintf("custom:%d", i), Name: "Relay", BaseURL: fmt.Sprintf("https://relay%d.example/v1", i), APIKey: &key, Model: "m"}
		if err := a.changeEndpoints(func(e []ai.CustomEndpoint) []ai.CustomEndpoint {
			e, _ = ai.SaveEndpoint(e, edit, ai.NewCustomID)
			return e
		}); err != nil {
			t.Error(err)
		}
	}
	save(0)
	var wg sync.WaitGroup
	for i := 1; i <= 24; i++ {
		wg.Go(func() { save(i) })
	}
	wg.Go(func() {
		a.changeEndpoints(func(e []ai.CustomEndpoint) []ai.CustomEndpoint {
			var kept []ai.CustomEndpoint
			for _, x := range e {
				if x.ID != "custom:0" {
					kept = append(kept, x)
				}
			}
			return kept
		})
	})
	wg.Wait()
	kept := a.customEndpoints()
	if len(kept) != 24 {
		t.Fatalf("%d endpoints", len(kept))
	}
	for _, e := range kept {
		if e.ID == "custom:0" || e.APIKey == nil {
			t.Errorf("%+v", e)
		}
	}
	if info, err := os.Stat(a.endpointsPath()); err != nil || !ownerOnly(info) {
		t.Errorf("%v %v", info.Mode(), err)
	}
	// The last one removed takes the file with it.
	a.changeEndpoints(func([]ai.CustomEndpoint) []ai.CustomEndpoint { return nil })
	if _, err := os.Stat(a.endpointsPath()); !os.IsNotExist(err) {
		t.Error("an empty list was kept")
	}
}

func TestNothingIsSentToAnAIServiceWhileTextusAIIsOff(t *testing.T) {
	off := &App{defaults: map[string]bool{"textusAi": false}}
	if err := off.ensureAIOn(); err == nil || err.Error() != textusAIOff {
		t.Error(err)
	}
	on := &App{defaults: map[string]bool{"textusAi": true}}
	if err := on.ensureAIOn(); err != nil {
		t.Error(err)
	}
}
