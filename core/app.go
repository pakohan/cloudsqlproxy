package main

import (
	"context"
	"log"
	"sync/atomic"
	"time"
)

// connectAfterSignIn is set when connecting failed for lack of credentials, so
// the proxies come up right after the user signed in.
var connectAfterSignIn atomic.Bool

func connect() {
	if err := mgr.Connect(); err != nil {
		log.Printf("Failed to connect: %v", err)
		connectAfterSignIn.Store(true)
		return
	}
	// Listeners start without a token, so check the credentials right away
	// instead of waiting for the first client to fail.
	checkCredentials()
}

// afterSignIn picks up the credentials of a new login and restarts the proxies
// with them.
func afterSignIn() error {
	if err := cred.Reload(); err != nil {
		log.Printf("Sign-in did not produce usable credentials: %v", err)
		return err
	}
	log.Println("Signed in, reloaded credentials")
	if err := mgr.Reconnect(); err != nil {
		log.Printf("Failed to reconnect: %v", err)
	}
	if connectAfterSignIn.Swap(false) {
		connect()
	}
	return nil
}

// watchCredentials periodically checks the credentials while connected, so an
// expired login is noticed before a client connection fails.
func watchCredentials() {
	for range time.Tick(5 * time.Minute) {
		if connected, _ := mgr.Status(); connected {
			checkCredentials()
		}
	}
}

func checkCredentials() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := cred.Check(ctx); err != nil {
		log.Printf("Credential check failed: %v", err)
	}
}
