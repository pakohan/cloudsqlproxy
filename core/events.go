package main

/*
#include <stdlib.h>

typedef void (*core_event_fn)(int event, const char *msg);

static void core_send_event(core_event_fn fn, int event, const char *msg) {
	fn(event, msg);
}
*/
import "C"

import (
	"sync"
	"unsafe"
)

var (
	eventMu sync.Mutex
	eventFn C.core_event_fn
)

func setEventHandler(fn C.core_event_fn) {
	eventMu.Lock()
	eventFn = fn
	eventMu.Unlock()
}

// sendEvent calls the app's event handler. It runs on the calling goroutine,
// so the app must hop to its main thread itself.
func sendEvent(event C.int, msg string) {
	eventMu.Lock()
	fn := eventFn
	eventMu.Unlock()
	if fn == nil {
		return
	}
	cmsg := C.CString(msg)
	defer C.free(unsafe.Pointer(cmsg))
	C.core_send_event(fn, event, cmsg)
}
