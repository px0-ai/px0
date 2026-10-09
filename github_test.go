package main

import "testing"

func TestToPRCommentOutdated(t *testing.T) {
	cases := []struct {
		name     string
		in       ghReviewComment
		line     int
		outdated bool
	}{
		{"current", ghReviewComment{Line: 5, OriginalLine: 5}, 5, false},
		{"moved but still anchored", ghReviewComment{Line: 9, OriginalLine: 5}, 9, false},
		{"outdated", ghReviewComment{Line: 0, OriginalLine: 12}, 12, true},
		{"file level", ghReviewComment{}, 0, false},
	}
	for _, c := range cases {
		got := c.in.toPRComment()
		if got.Line != c.line || got.Outdated != c.outdated {
			t.Errorf("%s: line=%d outdated=%v, want line=%d outdated=%v", c.name, got.Line, got.Outdated, c.line, c.outdated)
		}
	}
}
