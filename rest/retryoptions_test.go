package rest

import "testing"

func TestRetryOptionsValidate(t *testing.T) {
	ok := DefaultRetryOptions()
	cases := []struct {
		name string
		opts RetryOptions
		want bool
	}{
		{"default", ok, true},
		{"negative MaxAttempts", RetryOptions{MaxAttempts: -1, BaseBackoff: ok.BaseBackoff, MaxBackoff: ok.MaxBackoff, MaxDuration: ok.MaxDuration}, false},
		{"zero MaxAttempts is valid", RetryOptions{MaxAttempts: 0, BaseBackoff: ok.BaseBackoff, MaxBackoff: ok.MaxBackoff, MaxDuration: ok.MaxDuration}, true},
		{"zero value is invalid even with MaxAttempts at its own zero", RetryOptions{}, false},
		{"zero BaseBackoff", RetryOptions{MaxAttempts: 1, BaseBackoff: 0, MaxBackoff: ok.MaxBackoff, MaxDuration: ok.MaxDuration}, false},
		{"MaxBackoff below BaseBackoff", RetryOptions{MaxAttempts: 1, BaseBackoff: ok.BaseBackoff, MaxBackoff: ok.BaseBackoff / 2, MaxDuration: ok.MaxDuration}, false},
		{"zero MaxDuration", RetryOptions{MaxAttempts: 1, BaseBackoff: ok.BaseBackoff, MaxBackoff: ok.MaxBackoff, MaxDuration: 0}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.opts.Validate()
			if (err == nil) != c.want {
				t.Errorf("Validate() error = %v, want ok=%v", err, c.want)
			}
		})
	}
}
