package main

import (
	"errors"
	"testing"
)

func TestRequireBaseApplication(t *testing.T) {
	failure := errors.New("T3 is unavailable")
	if err := requireBaseApplication("base", func() error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("base publication must reject a failed T3 probe: %v", err)
	}
	called := false
	if err := requireBaseApplication("base", func() error { called = true; return nil }); err != nil || !called {
		t.Fatalf("base must pass a T3 probe: called=%v err=%v", called, err)
	}
	for _, name := range []string{"linux-base", "t3-base", "custom"} {
		if err := requireBaseApplication(name, func() error { t.Fatalf("legacy %s unexpectedly probed", name); return nil }); err != nil {
			t.Fatal(err)
		}
	}
}
