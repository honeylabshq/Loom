package buildinfo

import "testing"

func TestResolveOrder(t *testing.T) {
	const a = "0123456789abcdef0123456789abcdef01234567"
	const b = "89abcdef0123456789abcdef0123456789abcdef"
	none := func() (string, bool) { return "", false }
	vcs := func() (string, bool) { return b, false }
	dirty := func() (string, bool) { return b, true }
	cases := []struct {
		name, linked, archive string
		vcs                   func() (string, bool)
		want                  string
	}{
		{"linker flag wins", a, b, vcs, a},
		{"short linker flag", "3d9d463", "", none, "3d9d463"},
		{"dirty linker flag kept", a + "-dirty", "", none, a + "-dirty"},
		{"archive when not linked", "", b + "\n", none, b},
		{"unexpanded archive placeholder ignored", "", "$Format:%H$\n", vcs, b},
		{"vcs stamp", "", "", vcs, b},
		{"vcs stamp of a modified tree", "", "", dirty, b + "-dirty"},
		{"garbage linker value ignored", "not a sha", "", vcs, b},
		{"nothing", "", "$Format:%H$", none, "unknown"},
	}
	for _, c := range cases {
		if got := resolve(c.linked, c.archive, c.vcs); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCommitNeverEmpty(t *testing.T) {
	if Commit() == "" {
		t.Fatal("Commit() returned an empty string")
	}
	if Started().IsZero() {
		t.Fatal("Started() is zero")
	}
}
