package kvstore

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrefix(t *testing.T) {
	tests := []struct {
		name      string
		prefix    string
		wantStart []byte
		wantEnd   []byte
	}{
		{
			name:      "empty prefix",
			prefix:    "",
			wantStart: nil,
			wantEnd:   nil,
		},
		{
			name:      "simple prefix",
			prefix:    "user:",
			wantStart: []byte("user:"),
			wantEnd:   []byte("user;"), // semicolon is the byte after colon
		},
		{
			name:      "single byte",
			prefix:    "a",
			wantStart: []byte("a"),
			wantEnd:   []byte("b"),
		},
		{
			name:      "trailing 0xff increments previous byte",
			prefix:    "a\xff",
			wantStart: []byte("a\xff"),
			wantEnd:   []byte("b"), // 'a' increments, trailing 0xff truncated
		},
		{
			name:      "all 0xff bytes - no upper bound possible",
			prefix:    "\xff\xff",
			wantStart: []byte("\xff\xff"),
			wantEnd:   nil, // no upper bound when all bytes are 0xff
		},
		{
			name:      "mixed with 0xff in middle",
			prefix:    "a\xffb",
			wantStart: []byte("a\xffb"),
			wantEnd:   []byte("a\xffc"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Prefix(tt.prefix)
			require.Equal(t, tt.wantStart, got.Start)
			require.Equal(t, tt.wantEnd, got.End)
		})
	}
}
