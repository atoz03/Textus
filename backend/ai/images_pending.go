package ai

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"textus/backend/ai/aicompat"
)

// Pictures being made, or made and not saved yet, by id: kept in memory
// for the frontend to fetch, bounded in count and bytes.

type slotState uint8

const (
	slotMaking slotState = iota
	slotReady
	slotFailed
)

// pictureSlot is a picture asked for: still being made, made, or not made
// and why.
type pictureSlot struct {
	state slotState
	image aicompat.GeneratedImage
	why   string
}

func (s pictureSlot) bytes() int {
	if s.state == slotReady {
		return len(s.image.Bytes)
	}
	return 0
}

type pendingPicture struct {
	id   string
	slot pictureSlot
}

// Reference is a picture a new one is drawn from, where the service takes
// one.
type Reference struct {
	// Like: an earlier picture whose people and style the new one keeps -
	// a new scene, not a change to that one. Otherwise the picture being
	// redone (`replace`), changed as the description says.
	Like    bool
	Picture aicompat.ImageAttachment
}

// likeNote is prefixed to the description of a picture drawn like
// another: services otherwise read an attached picture as one to change.
const likeNote = "A new picture, not a copy of the attached one: keep the people, animals and places from the attached picture looking as they do there, and keep its style. The new picture: "

// asked is what a pending picture was asked for, so one that wasn't made
// can be asked for again by itself. Kept for as long as its slot is.
type asked struct {
	job       ImageJob
	prompt    string
	aspect    aicompat.Aspect
	reference *Reference
}

// sent is the description and picture the service is sent.
func (a *asked) sent() (string, *aicompat.ImageAttachment) {
	switch {
	case a.reference == nil:
		return a.prompt, nil
	case a.reference.Like:
		return likeNote + a.prompt, &a.reference.Picture
	}
	return a.prompt, &a.reference.Picture
}

// making is a picture being made: its id, the request that asked, how to
// stop it.
type making struct {
	id, request string
	stop        context.CancelFunc
}

// pictures are the pictures waiting for a decision, newest last. Bounded
// so undecided pictures can't hold hundreds of megabytes; the oldest go
// first.
var pictures struct {
	sync.Mutex
	pending []pendingPicture
	asked   map[string]*asked
	making  []*making
}

var (
	// pendingCount is how many pictures are kept at most.
	pendingCount = 16
	nextPicture  atomic.Uint64
)

const (
	pendingBytes = 128 << 20
	// WaitForPicture is how long the frontend waits for a picture still
	// being made: the slowest services take about two minutes, and give up
	// at three.
	WaitForPicture = 200 * time.Second
	// stoppedPicture is a picture stopped before it was made.
	stoppedPicture = "Stopped before the picture was made."
	// pictureGone is a picture that is no longer kept.
	pictureGone = "This picture is no longer available - ask for it again."
)

// trimPictures lets the oldest pictures go until the list is within count and the
// bytes kept. One still being made stays: it holds no bytes yet, it is
// being paid for, and what waits on it would get nothing.
func trimPictures(pending []pendingPicture, count int) []pendingPicture {
	for {
		total := 0
		for _, p := range pending {
			total += p.slot.bytes()
		}
		if len(pending) <= count && total <= pendingBytes {
			return pending
		}
		oldest := -1
		for i, p := range pending {
			if p.slot.state != slotMaking {
				oldest = i
				break
			}
		}
		if oldest < 0 {
			return pending
		}
		pending = append(pending[:oldest], pending[oldest+1:]...)
	}
}

// slotOf is id's slot, nil for none. Called with pictures locked.
func slotOf(id string) *pictureSlot {
	for i := range pictures.pending {
		if pictures.pending[i].id == id {
			return &pictures.pending[i].slot
		}
	}
	return nil
}

// newPicture is a new picture's id, its slot marked as being made.
func newPicture() string {
	// Short and distinct: the model copies it into its text, and long ids
	// differing in their last digits were copied wrong.
	x := uint64(time.Now().UnixNano()) ^ (nextPicture.Add(1)-1)<<40
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	x ^= x >> 31
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	id := make([]byte, 6)
	for i := range id {
		id[i] = digits[(x>>(i*6))%36]
	}
	pictures.Lock()
	defer pictures.Unlock()
	pictures.pending = trimPictures(append(pictures.pending, pendingPicture{id: string(id)}), pendingCount)
	return string(id)
}

// stopLocked stops making picture id, if it still is: nothing more is paid
// for it. Called with pictures locked.
func stopLocked(id string) {
	kept := pictures.making[:0]
	for _, m := range pictures.making {
		if m.id == id {
			m.stop()
		} else {
			kept = append(kept, m)
		}
	}
	pictures.making = kept
}

// StopPictures stops the pictures request requestID asked for and are
// still being made; each is left failed, to be asked for again. One that
// came in before the stop (and is paid for) stays.
func StopPictures(requestID string) {
	pictures.Lock()
	defer pictures.Unlock()
	kept := pictures.making[:0]
	for _, m := range pictures.making {
		if m.request != requestID {
			kept = append(kept, m)
			continue
		}
		m.stop()
		if slot := slotOf(m.id); slot != nil && slot.state == slotMaking {
			*slot = pictureSlot{state: slotFailed, why: stoppedPicture}
		}
	}
	pictures.making = kept
}

// DiscardPicture lets a picture go: its proposal was rejected, or it was
// saved. One still being made is stopped, and nothing more is paid for it.
func DiscardPicture(id string) {
	pictures.Lock()
	defer pictures.Unlock()
	stopLocked(id)
	for i, p := range pictures.pending {
		if p.id == id {
			pictures.pending = append(pictures.pending[:i], pictures.pending[i+1:]...)
			break
		}
	}
}

// makePicture makes picture id from what was asked, in the background, its slot
// filled when it comes - unless it is stopped first.
func makePicture(id string, what *asked, request string) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &making{id: id, request: request, stop: cancel}
	pictures.Lock()
	if pictures.asked == nil {
		pictures.asked = map[string]*asked{}
	}
	pictures.asked[id] = what
	for i := range pictures.asked {
		if slotOf(i) == nil {
			delete(pictures.asked, i)
		}
	}
	pictures.making = append(pictures.making, m)
	pictures.Unlock()
	go func() {
		defer cancel()
		prompt, reference := what.sent()
		image, err := what.job.generate(ctx, prompt, what.aspect, reference)
		slot := pictureSlot{state: slotReady, image: image}
		if err != nil {
			slot = pictureSlot{state: slotFailed, why: err.Error()}
		}
		pictures.Lock()
		defer pictures.Unlock()
		// Stopped: what came of it is let go, as if it never came.
		at := -1
		for i, other := range pictures.making {
			if other == m {
				at = i
			}
		}
		if at < 0 {
			return
		}
		pictures.making = append(pictures.making[:at], pictures.making[at+1:]...)
		if waiting := slotOf(id); waiting != nil {
			*waiting = slot
		}
		pictures.pending = trimPictures(pictures.pending, int(^uint(0)>>1))
	}()
}

// finishPicture is what came of a picture being made. Nothing, if it was let go
// meanwhile (its proposal rejected while it was painting).
func finishPicture(id string, slot pictureSlot) {
	pictures.Lock()
	defer pictures.Unlock()
	if waiting := slotOf(id); waiting != nil {
		*waiting = slot
	}
	pictures.pending = trimPictures(pictures.pending, int(^uint(0)>>1))
}

// askedFor is what id was asked for, nil once it is gone.
func askedFor(id string) *asked {
	pictures.Lock()
	defer pictures.Unlock()
	return pictures.asked[id]
}

// RetryPicture asks again for a picture that wasn't made: its slot goes
// back to being made. An error when it is gone, or not one that failed.
func RetryPicture(id string) error {
	what := askedFor(id)
	if what == nil {
		return errorText(pictureGone)
	}
	pictures.Lock()
	slot := slotOf(id)
	switch {
	case slot == nil:
		pictures.Unlock()
		return errorText(pictureGone)
	case slot.state != slotFailed:
		pictures.Unlock()
		return errorText("This picture was made, or is being made.")
	}
	*slot = pictureSlot{state: slotMaking}
	pictures.Unlock()
	makePicture(id, what, "")
	return nil
}

// PicturePrompt is the description picture id was asked for with, while
// it is kept.
func PicturePrompt(id string) (string, bool) {
	what := askedFor(id)
	if what == nil {
		return "", false
	}
	return what.prompt, true
}

// RedrawPicture makes picture id again from the writer's changed prompt,
// with the same service, shape and reference. An error when it is gone or
// still being made.
func RedrawPicture(id, prompt string) error {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return errorText("Describe the picture to draw.")
	}
	before := askedFor(id)
	if before == nil {
		return errorText(pictureGone)
	}
	pictures.Lock()
	slot := slotOf(id)
	switch {
	case slot == nil:
		pictures.Unlock()
		return errorText(pictureGone)
	case slot.state == slotMaking:
		pictures.Unlock()
		return errorText("This picture is still being made.")
	}
	*slot = pictureSlot{state: slotMaking}
	pictures.Unlock()
	makePicture(id, &asked{job: before.job, prompt: prompt, aspect: before.aspect, reference: before.reference}, "")
	return nil
}

// ReadyPicture is a made picture's file contents and extension, for
// keeping it with its document; false while being made, or when it
// failed or is gone.
func ReadyPicture(id string) ([]byte, string, bool) {
	pictures.Lock()
	defer pictures.Unlock()
	slot := slotOf(id)
	if slot == nil || slot.state != slotReady {
		return nil, "", false
	}
	return slot.image.Bytes, slot.image.Ext, true
}

// KeepPicture keeps a picture found rather than made - a paper's web
// picture - in the store as a made one, and answers its id.
func KeepPicture(image aicompat.GeneratedImage) string {
	id := newPicture()
	finishPicture(id, pictureSlot{state: slotReady, image: image})
	return id
}

// RestorePicture puts back a picture kept with its document, made in an
// earlier run of the app, under its id - unless one by that id is waiting
// already.
func RestorePicture(id string, image aicompat.GeneratedImage) {
	pictures.Lock()
	defer pictures.Unlock()
	if slotOf(id) != nil {
		return
	}
	pictures.pending = trimPictures(append(pictures.pending, pendingPicture{id, pictureSlot{state: slotReady, image: image}}), pendingCount)
}

// nominalSize is the size a picture is asked for at aspect, as GPT image
// makes it: the frame's shape while it is made.
func nominalSize(aspect aicompat.Aspect) (uint32, uint32) {
	w, h, _ := strings.Cut(aicompat.GPTImageSize(aspect), "x")
	width, err1 := strconv.ParseUint(w, 10, 32)
	height, err2 := strconv.ParseUint(h, 10, 32)
	if err1 != nil || err2 != nil {
		return 1024, 1024
	}
	return uint32(width), uint32(height)
}

// GeneratedImageData is a pending picture as the frontend takes it
// (GeneratedImageData in src/platform/bindings/ai-agent.ts).
type GeneratedImageData struct {
	Mime       string `json:"mime"`
	Ext        string `json:"ext"`
	DataBase64 string `json:"dataBase64"`
	Width      uint32 `json:"width"`
	Height     uint32 `json:"height"`
}

// madePicture is picture id as a picture to send a service, for a new one
// drawn like it. False while it is being made: a new picture doesn't wait
// for it.
func madePicture(id string) (aicompat.ImageAttachment, bool) {
	pictures.Lock()
	defer pictures.Unlock()
	slot := slotOf(id)
	if slot == nil || slot.state != slotReady {
		return aicompat.ImageAttachment{}, false
	}
	return aicompat.ImageAttachment{Name: id + "." + slot.image.Ext, Mime: slot.image.Mime, DataBase64: base64.StdEncoding.EncodeToString(slot.image.Bytes)}, true
}

// LookPicture is what a picture's slot holds now: the picture, or why it
// wasn't made; done false while it is being made.
func LookPicture(id string) (data GeneratedImageData, err error, done bool) {
	pictures.Lock()
	defer pictures.Unlock()
	slot := slotOf(id)
	switch {
	case slot == nil:
		return GeneratedImageData{}, errorText(pictureGone), true
	case slot.state == slotMaking:
		return GeneratedImageData{}, nil, false
	case slot.state == slotFailed:
		return GeneratedImageData{}, errorText(slot.why), true
	}
	image := slot.image
	return GeneratedImageData{Mime: image.Mime, Ext: image.Ext, DataBase64: base64.StdEncoding.EncodeToString(image.Bytes), Width: image.Width, Height: image.Height}, nil, true
}

// ErrPictureTimeout is a picture still not made when its wait ran out.
var ErrPictureTimeout = errors.New("the picture took too long")

// AwaitPicture is picture id once it is made, waiting at most wait for it
// while it is being made; or why it wasn't made, in the service's words.
func AwaitPicture(ctx context.Context, id string, wait time.Duration) (GeneratedImageData, error) {
	deadline := time.Now().Add(wait)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		if data, err, done := LookPicture(id); done {
			return data, err
		}
		if !time.Now().Before(deadline) {
			return GeneratedImageData{}, ErrPictureTimeout
		}
		select {
		case <-ctx.Done():
			return GeneratedImageData{}, ctx.Err()
		case <-tick.C:
		}
	}
}

// errorText is an error that reads text.
type errorText string

func (e errorText) Error() string { return string(e) }
