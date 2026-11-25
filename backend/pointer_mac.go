//go:build darwin && !ios

package backend

import (
	"sync"

	"github.com/ebitengine/purego"
)

// The pointer's button on macOS, wherever the pointer is: CoreGraphics
// tells without the Accessibility consent.

const pointerTracked = true

const coreGraphicsPath = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"

// buttonState is CGEventSourceButtonState, loaded the first time; nil when
// CoreGraphics can't be.
var buttonState = sync.OnceValue(func() func(state int32, button uint32) bool {
	lib, err := purego.Dlopen(coreGraphicsPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return nil
	}
	var fn func(state int32, button uint32) bool
	purego.RegisterLibFunc(&fn, lib, "CGEventSourceButtonState")
	return fn
})

// leftButtonDown: state 0 is the combined session's, button 0 the left.
func leftButtonDown() bool {
	if fn := buttonState(); fn != nil {
		return fn(0, 0)
	}
	return false
}
