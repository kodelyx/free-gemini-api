package gemini

import "testing"

func TestNextStreamDelta_ExtendsSnapshot(t *testing.T) {
	sent := ""
	for _, snapshot := range []string{"The", "The history", "The history of tea"} {
		delta, newSent := nextStreamDelta(sent, snapshot)
		if delta == "" {
			t.Fatalf("expected a delta for snapshot %q", snapshot)
		}
		sent = newSent
	}
	if sent != "The history of tea" {
		t.Fatalf("unexpected projection: %q", sent)
	}
}

func TestNextStreamDelta_DuplicateSnapshotIgnored(t *testing.T) {
	sent := "A complete answer."
	if delta, newSent := nextStreamDelta(sent, sent); delta != "" || newSent != sent {
		t.Fatalf("duplicate snapshot must not emit, got delta=%q newSent=%q", delta, newSent)
	}
}

func TestNextStreamDelta_DivergentRerenderIgnored(t *testing.T) {
	// Old behaviour appended the whole snapshot here, duplicating the answer.
	sent := "The history of tea is long."
	delta, newSent := nextStreamDelta(sent, "Tea history is long and rich.")
	if delta != "" {
		t.Fatalf("divergent re-render should not be appended, got %q", delta)
	}
	if newSent != sent {
		t.Fatalf("sent text should be unchanged, got %q", newSent)
	}
}

func TestNextStreamDelta_EmptySnapshotIgnored(t *testing.T) {
	if delta, newSent := nextStreamDelta("abc", ""); delta != "" || newSent != "abc" {
		t.Fatalf("empty snapshot must be a no-op")
	}
}

func TestIsArtifactStreamText(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"http://googleusercontent.com/lmdx_content/abc123", true},
		{"https://googleusercontent.com/card_content/42", true},
		{"Read more at https://googleusercontent.com/card_content/42 for details", false},
		{"plain answer text", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isArtifactStreamText(c.in); got != c.want {
			t.Errorf("isArtifactStreamText(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
