package main

// The C API the SwiftUI app calls. It is built with -buildmode=c-archive into
// build/libcore.a and build/libcore.h.
//
// Conventions:
//   - Strings passed in are borrowed; Go copies them.
//   - Strings returned are allocated with malloc; the caller frees them.
//   - Functions that can fail return an error message, or NULL on success.
//   - Structured results are JSON.
//   - Calls that do network or listener work block; call them off the main
//     thread.

/*
#include <stdbool.h>
#include <stdlib.h>

// Events sent to the app. msg is only valid during the call.
enum {
	CORE_EVENT_STATE = 1,        // proxy state or config changed
	CORE_EVENT_LOGS = 2,         // new log lines
	CORE_EVENT_AUTH_INVALID = 3, // credentials stopped working; msg is the error
};
typedef void (*core_event_fn)(int event, const char *msg);
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

// The c-archive build mode requires a main function; it never runs.
func main() {}

var (
	cfg  *ConfigStore
	logs *LogBuffer
	cred *Auth
	mgr  *Manager

	signInMu     sync.Mutex
	cancelSignIn context.CancelFunc
)

//export CoreInit
func CoreInit(fn C.core_event_fn) *C.char {
	setEventHandler(fn)

	logPath, err := logFilePath()
	if err != nil {
		return cError(fmt.Errorf("determine log file path: %w", err))
	}
	if logs, err = NewLogBuffer(logPath); err != nil {
		return cError(fmt.Errorf("open log file: %w", err))
	}
	log.SetOutput(logs)
	logs.SetOnChange(func() { sendEvent(C.CORE_EVENT_LOGS, "") })

	cfgPath, err := defaultConfigPath()
	if err != nil {
		return cError(fmt.Errorf("determine config path: %w", err))
	}
	if cfg, err = LoadConfig(cfgPath); err != nil {
		return cError(fmt.Errorf("load config: %w", err))
	}

	cred = &Auth{OnInvalid: func(err error) { sendEvent(C.CORE_EVENT_AUTH_INVALID, err.Error()) }}
	mgr = NewManager(cfg, cred)
	mgr.OnChange = func() { sendEvent(C.CORE_EVENT_STATE, "") }

	if len(cfg.Enabled()) > 0 {
		go connect()
	}
	go watchCredentials()
	UpdateAutostart()
	return nil
}

// CoreShutdown stops all proxies before the app quits.
//
//export CoreShutdown
func CoreShutdown() {
	mgr.Disconnect()
}

//export CoreLog
func CoreLog(msg *C.char) {
	log.Print(C.GoString(msg))
}

// CoreStatus returns {"connected", "running", "enabled"} as JSON.
//
//export CoreStatus
func CoreStatus() *C.char {
	connected, running := mgr.Status()
	return cJSON(map[string]any{"connected": connected, "running": running, "enabled": len(cfg.Enabled())})
}

// CoreConnect starts the proxies of all enabled tunnels. It blocks.
//
//export CoreConnect
func CoreConnect() {
	connect()
}

//export CoreDisconnect
func CoreDisconnect() {
	mgr.Disconnect()
}

// CoreApply syncs one instance's proxy with its config while connected.
//
//export CoreApply
func CoreApply(instance *C.char) {
	mgr.Apply(C.GoString(instance))
}

// CoreConfig returns {"projects": [...], "tunnels": [...]} as JSON.
//
//export CoreConfig
func CoreConfig() *C.char {
	return cJSON(map[string]any{"projects": nonNil(cfg.Projects()), "tunnels": nonNil(cfg.Tunnels())})
}

//export CoreAddProject
func CoreAddProject(project *C.char) *C.char {
	return cError(cfg.AddProject(C.GoString(project)))
}

//export CoreRemoveProject
func CoreRemoveProject(project *C.char) *C.char {
	return cError(cfg.RemoveProject(C.GoString(project)))
}

// CoreSetTunnel saves a tunnel's port and enabled state. It doesn't touch the
// running proxy; call CoreApply for that.
//
//export CoreSetTunnel
func CoreSetTunnel(instance *C.char, port C.int, enabled C.bool) *C.char {
	err := cfg.Set(C.GoString(instance), int(port), bool(enabled))
	sendEvent(C.CORE_EVENT_STATE, "")
	return cError(err)
}

// CorePortConflict returns the name of another enabled tunnel on port, or an
// empty string.
//
//export CorePortConflict
func CorePortConflict(instance *C.char, port C.int) *C.char {
	return C.CString(cfg.PortConflict(C.GoString(instance), int(port)))
}

// CorePortConflicts returns a JSON array describing ports shared by enabled
// tunnels.
//
//export CorePortConflicts
func CorePortConflicts() *C.char {
	return cJSON(nonNil(cfg.PortConflicts()))
}

//export CoreSuggestPort
func CoreSuggestPort(instance *C.char) C.int {
	return C.int(cfg.SuggestPort(C.GoString(instance)))
}

// CoreListProjects returns every project the credentials can see as
// {"items": [{"id", "name"}], "error"}. It blocks for several seconds.
//
//export CoreListProjects
func CoreListProjects() *C.char {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var projects []Project
	creds, err := cred.Credentials()
	if err == nil {
		projects, err = ListProjects(ctx, creds)
	}
	if err != nil {
		log.Printf("Failed to list projects: %v", err)
	}
	return cResult(nonNil(projects), err)
}

// CoreListInstances returns the Cloud SQL instances of a project as
// {"items": [...], "error"}. It blocks.
//
//export CoreListInstances
func CoreListInstances(project *C.char) *C.char {
	p := C.GoString(project)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var instances []Instance
	creds, err := cred.Credentials()
	if err == nil {
		instances, err = ListInstances(ctx, creds, p)
	}
	if err != nil {
		log.Printf("Failed to list instances of %s: %v", p, err)
	}
	return cResult(nonNil(instances), err)
}

// CoreSignIn runs the gcloud login, then reloads the credentials and
// reconnects. It blocks until the login finishes and returns NULL on success.
// After CoreCancelSignIn it returns "cancelled".
//
//export CoreSignIn
func CoreSignIn() *C.char {
	ctx, cancel := context.WithCancel(context.Background())
	signInMu.Lock()
	if cancelSignIn != nil {
		cancelSignIn()
	}
	cancelSignIn = cancel
	signInMu.Unlock()
	defer cancel()

	if err := SignIn(ctx); err != nil {
		if ctx.Err() != nil {
			return C.CString("cancelled")
		}
		return cError(fmt.Errorf("sign-in failed: %w", err))
	}
	if err := afterSignIn(); err != nil {
		return cError(err)
	}
	return nil
}

//export CoreCancelSignIn
func CoreCancelSignIn() {
	signInMu.Lock()
	defer signInMu.Unlock()
	if cancelSignIn != nil {
		cancelSignIn()
		cancelSignIn = nil
	}
}

//export CoreLogs
func CoreLogs() *C.char {
	return C.CString(logs.String())
}

//export CoreClearLogs
func CoreClearLogs() {
	logs.Clear()
	sendEvent(C.CORE_EVENT_LOGS, "")
}

//export CoreLogPath
func CoreLogPath() *C.char {
	return C.CString(logs.Path())
}

//export CoreAutostartEnabled
func CoreAutostartEnabled() C.bool {
	return C.bool(AutostartEnabled())
}

//export CoreSetAutostart
func CoreSetAutostart(enabled C.bool) *C.char {
	err := SetAutostart(bool(enabled))
	if err != nil {
		log.Printf("Failed to change start at login: %v", err)
	}
	return cError(err)
}

func cError(err error) *C.char {
	if err == nil {
		return nil
	}
	return C.CString(err.Error())
}

func cJSON(v any) *C.char {
	b, err := json.Marshal(v)
	if err != nil {
		// Only plain data is marshalled, so this is a programming error.
		panic(err)
	}
	return C.CString(string(b))
}

func cResult(items any, err error) *C.char {
	res := map[string]any{"items": items}
	if err != nil {
		res["error"] = err.Error()
	}
	return cJSON(res)
}

// nonNil makes nil slices marshal as [] instead of null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
