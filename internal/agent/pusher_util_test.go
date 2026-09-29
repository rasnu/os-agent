// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"testing"
)

func TestPusherUtil_Types(t *testing.T) {
	m := map[string]interface{}{
		"str":  "hello",
		"bool": true,
		"num":  42.5,
		"bad":  struct{}{},
	}

	if getString(m, "str") != "hello" {
		t.Error("expected hello")
	}
	if getString(m, "bad") != "" {
		t.Error("expected empty string for bad type")
	}
	if getString(m, "missing") != "" {
		t.Error("expected empty string for missing")
	}

	if !getBool(m, "bool") {
		t.Error("expected true")
	}
	if getBool(m, "bad") {
		t.Error("expected false for bad type")
	}
	if getBool(m, "missing") {
		t.Error("expected false for missing")
	}

	if getFloat(m, "num") != 42.5 {
		t.Error("expected 42.5")
	}
	if getFloat(m, "bad") != 0 {
		t.Error("expected 0 for bad type")
	}
	if getFloat(m, "missing") != 0 {
		t.Error("expected 0 for missing")
	}
}

func TestPusherUtil_SafeWriteError(t *testing.T) {
	client := NewPusherClient("ws://", "http://localhost/api", "token", "srv", "")
	client.safeWriteMessage(1, []byte("hello"))
}
