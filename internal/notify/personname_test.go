package notify

import "testing"

func TestPersonName(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{[]string{"PlexSam", "sam@example.com"}, "PlexSam"},
		{[]string{"", "  ", "sam@example.com"}, "sam"},
		{[]string{"sam"}, "sam"},
		{[]string{"@example.com"}, "Someone"},
		{[]string{"", ""}, ""},
		{nil, ""},
	} {
		if got := PersonName(tc.in...); got != tc.want {
			t.Errorf("PersonName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
