package shortcut

import "testing"

func TestMergeMessageLinksRequestedText(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "group merge flag",
			text: "https://t.me/example/1\nhttps://t.me/example/2\ngroup:merge",
			want: true,
		},
		{
			name: "merge command",
			text: "/merge https://t.me/example/1 https://t.me/example/2",
			want: true,
		},
		{
			name: "merge command glued to link",
			text: "/mergehttps://t.me/example/1https://t.me/example/2",
			want: true,
		},
		{
			name: "merge command with bot username",
			text: "/merge@saveanybot https://t.me/example/1",
			want: true,
		},
		{
			name: "case insensitive",
			text: "GROUP:MERGE",
			want: true,
		},
		{
			name: "ordinary multiple links",
			text: "https://t.me/example/1\nhttps://t.me/example/2",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MergeMessageLinksRequestedText(tt.text); got != tt.want {
				t.Fatalf("MergeMessageLinksRequestedText() = %v, want %v", got, tt.want)
			}
		})
	}
}
