package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
)

// Pictures from Textus AI: the request's image model resolved with the
// app's keys and sign-in, the commands the document's picture frames use,
// and the made pictures kept beside a document's kept proposals.

// imageRun is the request's image run with its image model, made beside
// the document and stopped with the request; false without one.
func (a *App) imageRun(choice *ai.ImageModel, chatProvider, docPath, requestID string) (*ai.ImageRun, bool) {
	_, signedIn, _ := a.loadSignIn(chatGPTSignIn)
	run, ok := ai.ResolveImageRun(choice, chatProvider, ai.ImageAccess{
		ChatGPTSignedIn: signedIn,
		Key: func(id string) string {
			key, _ := a.providerKey(id)
			return strings.TrimSpace(key)
		},
		SignIn: func(ctx context.Context) (string, string, error) {
			cred, err := a.validSignIn(ctx, chatGPTSignIn)
			return cred.Access, cred.AccountID, err
		},
		Client: netClient(0),
	})
	if !ok {
		return nil, false
	}
	if docPath != "" {
		run.InFolder(func(src string) (aicompat.ImageAttachment, bool) { return localPicture(filepath.Dir(docPath), src) })
	}
	return run.ForRequest(requestID), true
}

// localPicture is the picture src names in the document's folder, as a
// service is sent it; false outside the folder or when it isn't one.
func localPicture(folder, src string) (aicompat.ImageAttachment, bool) {
	image, err := readImage(folder, src)
	return image, err == nil
}

// pictureExts are the files a kept picture is.
var pictureExts = []string{".png", ".jpg", ".jpeg", ".webp"}

// keptPictures are the kept picture files in dir, by id.
func keptPictures(dir string) map[string]string {
	entries, _ := os.ReadDir(dir)
	kept := map[string]string{}
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		id := strings.TrimSuffix(e.Name(), ext)
		if keptPictureID(id) && slices.Contains(pictureExts, ext) && e.Type().IsRegular() {
			kept[id] = filepath.Join(dir, e.Name())
		}
	}
	return kept
}

// keepPictures keeps the made pictures among ids in dir and lets go of the
// rest. One still being made is kept on a later write. Only picture files
// are ever removed, and the folder once empty.
func keepPictures(dir string, ids []string) error {
	for id, path := range keptPictures(dir) {
		if !slices.Contains(ids, id) {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	have := keptPictures(dir)
	for _, id := range ids {
		if _, ok := have[id]; ok || !keptPictureID(id) {
			continue
		}
		if bytes, ext, ok := ai.ReadyPicture(id); ok {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			if err := writeFileAtomic(filepath.Join(dir, id+"."+ext), bytes); err != nil {
				return err
			}
		}
	}
	if left, err := os.ReadDir(dir); err == nil && len(left) == 0 {
		return os.Remove(dir)
	}
	return nil
}

// restorePictures puts the pictures kept in dir back among the waiting
// ones, under their ids.
func restorePictures(dir string) {
	for id, path := range keptPictures(dir) {
		if raw, err := os.ReadFile(path); err == nil {
			if image, err := aicompat.CheckedImage(raw); err == nil {
				ai.RestorePicture(id, image)
			}
		}
	}
}

func init() {
	aicompat.PictureNet.Proxy = proxyURL
	command("list_image_providers", func(c *Call, _ none) ([]ai.ImageProviderEntry, error) {
		return ai.ImageProviders, nil
	})
	// A picture, once it is made: the frame in the document waits on this.
	// Its error is why it wasn't made, in the service's words.
	command("ai_generated_image", func(c *Call, a struct {
		ID string `json:"id"`
	}) (ai.GeneratedImageData, error) {
		ctx := c.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		data, err := ai.AwaitPicture(ctx, a.ID, ai.WaitForPicture)
		if errors.Is(err, ai.ErrPictureTimeout) {
			return data, codeError("imageServiceTimeout")
		}
		return data, err
	})
	// Asks again for a picture that wasn't made - the same description, to
	// the same service - leaving the others alone; ai_generated_image then
	// waits for it as for any picture.
	command("ai_retry_generated_image", func(c *Call, a struct {
		ID string `json:"id"`
	}) (nothing, error) {
		// A new picture is paid for: not while Textus AI is off.
		if err := c.app.ensureAIOn(); err != nil {
			return nil, err
		}
		return nil, ai.RetryPicture(a.ID)
	})
	// The description a proposed picture was drawn from, for the writer to
	// change; null once it is gone.
	command("ai_generated_image_prompt", func(c *Call, a struct {
		ID string `json:"id"`
	}) (*string, error) {
		prompt, ok := ai.PicturePrompt(a.ID)
		if !ok {
			return nil, nil
		}
		return &prompt, nil
	})
	// Draws a proposed picture again from the writer's description, leaving
	// the others alone.
	command("ai_redraw_generated_image", func(c *Call, a struct {
		ID     string `json:"id"`
		Prompt string `json:"prompt"`
	}) (nothing, error) {
		if err := c.app.ensureAIOn(); err != nil {
			return nil, err
		}
		return nil, ai.RedrawPicture(a.ID, a.Prompt)
	})
	// Lets a picture go: its proposal was rejected, or it was saved. One
	// still being made is stopped, and nothing more is paid for it.
	command("ai_discard_generated_image", func(c *Call, a struct {
		ID string `json:"id"`
	}) (nothing, error) {
		ai.DiscardPicture(a.ID)
		return nil, nil
	})
}
