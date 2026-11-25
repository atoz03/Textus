package backend

import (
	"slices"
	"testing"
)

func TestADetachedChatIsNeverTakenForAnEditor(t *testing.T) {
	if label := chatLabelPrefix + "7"; isEditorWindow(label) || kindOf(label) != kindChat {
		t.Errorf("%s", label)
	}
}

func TestAChatResolvesBackToItsEditorAndServesOnlyIt(t *testing.T) {
	var chats chatWindows
	chats.add("chat-1", "main")
	chats.add("chat-3", "window-2")
	for chat, editor := range map[string]string{"chat-1": "main", "chat-3": "window-2"} {
		if got, ok := chats.editorFor(chat); !ok || got != editor {
			t.Errorf("%s: got %s", chat, got)
		}
	}
	if _, ok := chats.editorFor("chat-9"); ok {
		t.Error("an unknown chat has an editor")
	}
	if chat, ok := chats.serving("main"); !ok || chat != "chat-1" {
		t.Errorf("got %s", chat)
	}
	if _, ok := chats.serving("window-5"); ok {
		t.Error("a chat serves another editor")
	}
}

func TestForgettingAWindowClearsBothWaysAndAHandoffIsTakenOnce(t *testing.T) {
	var chats chatWindows
	chats.add("chat-1", "main")
	chats.add("chat-3", "window-2")
	// A closed chat drops its own entry, freeing its editor to detach again.
	if orphans, changed := chats.forget("chat-1"); !changed || len(orphans) != 0 {
		t.Errorf("got %v, %v", orphans, changed)
	}
	if _, ok := chats.serving("main"); ok {
		t.Error("the closed chat still serves")
	}
	// A closed editor drops its chat, which closes with it.
	if orphans, changed := chats.forget("window-2"); !changed || !slices.Equal(orphans, []string{"chat-3"}) {
		t.Errorf("got %v, %v", orphans, changed)
	}
	if _, changed := chats.forget("window-9"); changed {
		t.Error("forgot what wasn't there")
	}
	chats.park("chat-4", chatHandoff{EditorLabel: "main", State: []byte(`{"turns":[]}`)})
	if h := chats.take("chat-4"); h == nil || h.EditorLabel != "main" || string(h.State) != `{"turns":[]}` {
		t.Errorf("got %+v", h)
	}
	if h := chats.take("chat-4"); h != nil {
		t.Error("taken twice")
	}
}
