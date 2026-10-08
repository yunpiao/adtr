package directoryassets

import (
	"bytes"
	"errors"
	"testing"
)

func TestDiscardClearsRetainedCookieAndClosesObservation(t *testing.T) {
	a, err := NewAccumulator(Limits{MaxRows: 1, MaxPages: 2, MaxPageEntries: 1, MaxBytes: 1024, MaxCookieBytes: 32})
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("opaque-page-state")
	if err := a.AddPage(nil, nil, input); err != nil {
		t.Fatal(err)
	}
	retained := a.cookie
	a.Discard()
	a.Discard()
	if !bytes.Equal(retained, make([]byte, len(retained))) {
		t.Fatal("retained cookie survived discard")
	}
	if !bytes.Equal(input, []byte("opaque-page-state")) {
		t.Fatal("discard changed caller-owned cookie")
	}
	if result, err := a.Result(); result != nil || !errors.Is(err, ErrClosed) {
		t.Fatalf("result survived discard: %v", err)
	}
	if err := a.AddPage(nil, nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("discarded observation accepted another page: %v", err)
	}
}

func TestDiscardClearsCookieDuringPanicCleanup(t *testing.T) {
	a, err := NewAccumulator(Limits{MaxRows: 1, MaxPages: 2, MaxPageEntries: 1, MaxBytes: 1024, MaxCookieBytes: 32})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.AddPage(nil, nil, []byte("opaque-page-state")); err != nil {
		t.Fatal(err)
	}
	retained := a.cookie
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic fixture did not run")
			}
		}()
		defer a.Discard()
		panic("synthetic consumer panic")
	}()
	if !bytes.Equal(retained, make([]byte, len(retained))) {
		t.Fatal("retained cookie survived panic cleanup")
	}
	if result, err := a.Result(); result != nil || !errors.Is(err, ErrClosed) {
		t.Fatalf("partial result after panic: %v", err)
	}
}

func TestDiscardAfterCompleteAndNil(t *testing.T) {
	var absent *Accumulator
	absent.Discard()
	a, err := NewAccumulator(Limits{MaxRows: 1, MaxPages: 1, MaxPageEntries: 1, MaxBytes: 1024, MaxCookieBytes: 32})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.AddPage(nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	result, err := a.Result()
	if err != nil || result == nil {
		t.Fatal("terminal result was not complete")
	}
	a.Discard()
	if _, err := a.Result(); !errors.Is(err, ErrClosed) {
		t.Fatalf("result available after discard: %v", err)
	}
}
