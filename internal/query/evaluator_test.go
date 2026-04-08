package query

import "testing"

func TestMatchPatternNormalizesLeadingSlashes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{
			name:    "relative pattern",
			pattern: "src/pure/**",
			path:    "src/pure/math.ts",
			want:    true,
		},
		{
			name:    "leading slash pattern",
			pattern: "/src/pure/**",
			path:    "src/pure/math.ts",
			want:    true,
		},
		{
			name:    "leading slash path",
			pattern: "src/pure/**",
			path:    "/src/pure/math.ts",
			want:    true,
		},
		{
			name:    "non match",
			pattern: "/src/services/**",
			path:    "src/pure/math.ts",
			want:    false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := matchPattern(tc.pattern, tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("matchPattern(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}
