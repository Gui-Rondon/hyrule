package main

import "testing"

func TestRunDispatch(t *testing.T) {
	if err := run(nil); err == nil {
		t.Error("no command should be an error")
	}
	if err := run([]string{"bogus"}); err == nil {
		t.Error("an unknown command should be an error")
	}
	if err := run([]string{"help"}); err != nil {
		t.Errorf("help = %v", err)
	}
}
