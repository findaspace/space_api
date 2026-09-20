package identity

import "testing"

func TestNormalizePhone(t *testing.T) {
	valid := []struct {
		in, want string
	}{
		{"0244123456", "+233244123456"},
		{"+233244123456", "+233244123456"},
		{"233244123456", "+233244123456"},
		{"024 412 3456", "+233244123456"},
		{"+233 (024) 412-3456", "+233244123456"},
	}

	for _, tc := range valid {
		got, err := NormalizePhone(tc.in)
		if err != nil {
			t.Errorf("NormalizePhone(%q): unexpected error: %v", tc.in, err)
			continue
		}

		if got != tc.want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	invalid := []string{
		"",
		"12",
		"not a number",
		"+233000000000",
	}

	for _, tc := range invalid {
		got, err := NormalizePhone(tc)
		if err == nil {
			t.Errorf("NormalizePhone(%q) = %q, want error", tc, got)
		}
	}
}

func TestMaskPhone(t *testing.T) {
	if got := MaskPhone("+233244123456"); got != "+233244******" {
		t.Errorf("MaskPhone = %q", got)
	}
}
