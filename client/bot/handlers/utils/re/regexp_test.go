package re

import "testing"

func TestExtractTgMessageLinks(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "space separated links",
			text: "https://t.me/example/1 https://t.me/example/2",
			want: []string{"https://t.me/example/1", "https://t.me/example/2"},
		},
		{
			name: "adjacent links",
			text: "/merge https://t.me/example/1https://t.me/example/2",
			want: []string{"https://t.me/example/1", "https://t.me/example/2"},
		},
		{
			name: "merge command glued to adjacent links",
			text: "/mergehttps://t.me/example/1https://t.me/c/123456/2",
			want: []string{"https://t.me/example/1", "https://t.me/c/123456/2"},
		},
		{
			name: "telegram domain and query",
			text: "https://telegram.me/example/1?singlehttps://t.me/example/2",
			want: []string{"https://telegram.me/example/1?single", "https://t.me/example/2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractTgMessageLinks(tt.text)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d links %#v, want %d %#v", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("link %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
